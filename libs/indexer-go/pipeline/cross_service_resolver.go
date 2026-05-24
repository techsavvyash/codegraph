package pipeline

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// CrossServiceHandlerResolver matches GRPCCall and HTTPCall nodes that have a
// CALLS_SERVICE edge to an already-indexed Service, then writes a RESOLVES_TO
// edge to the concrete handler Function/Method in that service.
//
// Confidence levels:
//   - 0.9  proto_matched      — exactly one Function with that method name found
//   - 0.6  heuristic          — multiple candidates; first (alphabetically lowest nodeKey) chosen
//   - 0.85 http_route_matched — APIRoute path+method uniquely identifies a handler
//   - 0.6  heuristic          — multiple HTTP candidates; best path-overlap chosen
type CrossServiceHandlerResolver struct {
	client   *neo4j.Client
	scopeCtx models.ScopeContext
}

// NewCrossServiceHandlerResolver creates a resolver scoped to the given Neo4j client.
func NewCrossServiceHandlerResolver(client *neo4j.Client) *CrossServiceHandlerResolver {
	return &CrossServiceHandlerResolver{client: client}
}

// SetScope configures the tenant/scope context used to narrow queries.
func (r *CrossServiceHandlerResolver) SetScope(ctx models.ScopeContext) {
	r.scopeCtx = ctx
}

// Resolve finds all resolvable cross-service call sites and writes RESOLVES_TO edges.
// Returns the total number of edges written.
func (r *CrossServiceHandlerResolver) Resolve(ctx context.Context) (int, error) {
	grpcCount, err := r.resolveGRPC(ctx)
	if err != nil {
		log.Printf("[CrossServiceResolver] gRPC resolution error: %v", err)
	}

	httpCount, err := r.resolveHTTP(ctx)
	if err != nil {
		log.Printf("[CrossServiceResolver] HTTP resolution error: %v", err)
	}

	total := grpcCount + httpCount
	log.Printf("[CrossServiceResolver] Wrote %d RESOLVES_TO edges (%d gRPC, %d HTTP)", total, grpcCount, httpCount)
	return total, nil
}

// resolveGRPC processes GRPCCall → CALLS_SERVICE → Service edges and writes
// RESOLVES_TO edges to the best-matching handler Function in the target service.
func (r *CrossServiceHandlerResolver) resolveGRPC(ctx context.Context) (int, error) {
	// Fetch all GRPCCall nodes that already have a resolved target service.
	callsQuery := `
		MATCH (gc:GRPCCall)-[:CALLS_SERVICE]->(svc:Service)
		WHERE gc.targetMethod IS NOT NULL AND gc.targetMethod <> ''
		RETURN elementId(gc) AS gcId,
		       gc.targetMethod AS targetMethod,
		       elementId(svc) AS svcId,
		       svc.name AS svcName,
		       coalesce(gc.protoService, '') AS protoService
	`
	rows, err := r.client.ExecuteQuery(ctx, callsQuery, map[string]any{})
	if err != nil {
		return 0, fmt.Errorf("resolveGRPC: query CALLS_SERVICE: %w", err)
	}

	written := 0
	for _, row := range rows {
		rm := row.AsMap()
		gcId := getString(rm, "gcId")
		targetMethod := getString(rm, "targetMethod")
		svcId := getString(rm, "svcId")

		if gcId == "" || svcId == "" || targetMethod == "" {
			continue
		}

		// targetMethod format: "ServiceName.MethodName" or just "MethodName"
		methodName := extractMethodName(targetMethod)
		if methodName == "" {
			continue
		}

		// Two-pass resolution: try proto server type match first for high confidence.
		protoService := getString(rm, "protoService")
		var handlers []*neo4jdriver.Record
		confidence := 0.7
		resolutionMethod := "name_match"

		if protoService != "" {
			protoServerType := protoService + "Server"
			protoHandlerQuery := `
				MATCH (svc)-[:CONTAINS*1..5]->(f)
				WHERE elementId(svc) = $svcId
				  AND (f:Function OR f:Method)
				  AND f.name = $methodName
				  AND f.receiverType = $protoServerType
				RETURN elementId(f) AS fId, f.name AS fName, f.nodeKey AS nodeKey
				ORDER BY f.nodeKey ASC
				LIMIT 3
			`
			protoHandlers, protoErr := r.client.ExecuteQuery(ctx, protoHandlerQuery, map[string]any{
				"svcId":           svcId,
				"methodName":      methodName,
				"protoServerType": protoServerType,
			})
			if protoErr == nil && len(protoHandlers) > 0 {
				handlers = protoHandlers
				confidence = 1.0
				resolutionMethod = "proto"
			}
		}

		// Fall back to name-only query if proto match found nothing.
		if len(handlers) == 0 {
			nameHandlerQuery := `
				MATCH (svc)-[:CONTAINS*1..5]->(f)
				WHERE elementId(svc) = $svcId
				  AND (f:Function OR f:Method)
				  AND f.name = $methodName
				RETURN elementId(f) AS fId, f.name AS fName, f.nodeKey AS nodeKey
				ORDER BY f.nodeKey ASC
				LIMIT 5
			`
			nameHandlers, nameErr := r.client.ExecuteQuery(ctx, nameHandlerQuery, map[string]any{
				"svcId":      svcId,
				"methodName": methodName,
			})
			if nameErr != nil || len(nameHandlers) == 0 {
				continue
			}
			handlers = nameHandlers
			if len(handlers) == 1 {
				confidence = 0.7
			} else {
				confidence = 0.6
				resolutionMethod = "heuristic"
			}
		}

		best := handlers[0].AsMap()
		fId := getString(best, "fId")
		if fId == "" {
			continue
		}

		if err := r.writeResolvesToEdge(ctx, gcId, fId, confidence, resolutionMethod); err != nil {
			log.Printf("[CrossServiceResolver] RESOLVES_TO write failed (%s → %s): %v", gcId, fId, err)
			continue
		}
		written++
	}
	return written, nil
}

// resolveHTTP processes HTTPCall → CALLS_SERVICE → Service edges and writes
// RESOLVES_TO edges to the handler Function that EXPOSES_API the matching APIRoute.
func (r *CrossServiceHandlerResolver) resolveHTTP(ctx context.Context) (int, error) {
	callsQuery := `
		MATCH (hc:HTTPCall)-[:CALLS_SERVICE]->(svc:Service)
		WHERE hc.url IS NOT NULL AND hc.url <> '' AND hc.url <> 'dynamic'
		RETURN elementId(hc) AS hcId,
		       hc.url AS url,
		       hc.method AS httpMethod,
		       elementId(svc) AS svcId,
		       svc.name AS svcName
	`
	rows, err := r.client.ExecuteQuery(ctx, callsQuery, map[string]any{})
	if err != nil {
		return 0, fmt.Errorf("resolveHTTP: query CALLS_SERVICE: %w", err)
	}

	written := 0
	for _, row := range rows {
		rm := row.AsMap()
		hcId := getString(rm, "hcId")
		rawURL := getString(rm, "url")
		httpMethod := strings.ToUpper(getString(rm, "httpMethod"))
		svcId := getString(rm, "svcId")

		if hcId == "" || svcId == "" || rawURL == "" {
			continue
		}

		urlPath := extractURLPath(rawURL)
		if urlPath == "" {
			continue
		}
		if httpMethod == "" {
			httpMethod = "ANY"
		}

		// Find handler Functions that EXPOSES_API to a matching APIRoute in the target service.
		handlerQuery := `
			MATCH (svc)-[:CONTAINS*1..5]->(f)-[:EXPOSES_API]->(ar:APIRoute)
			WHERE elementId(svc) = $svcId
			  AND (f:Function OR f:Method)
			  AND (toLower(ar.path) CONTAINS toLower($urlPath)
			       OR toLower($urlPath) CONTAINS toLower(ar.path))
			  AND ($httpMethod = 'ANY' OR ar.method = $httpMethod OR ar.method IS NULL)
			RETURN elementId(f) AS fId, f.name AS fName, ar.path AS routePath
			ORDER BY size(ar.path) DESC
			LIMIT 5
		`
		handlers, err := r.client.ExecuteQuery(ctx, handlerQuery, map[string]any{
			"svcId":      svcId,
			"urlPath":    urlPath,
			"httpMethod": httpMethod,
		})
		if err != nil || len(handlers) == 0 {
			continue
		}

		confidence, resolutionMethod := scoreHTTPMatch(len(handlers))
		best := handlers[0].AsMap()
		fId := getString(best, "fId")
		if fId == "" {
			continue
		}

		if err := r.writeResolvesToEdge(ctx, hcId, fId, confidence, resolutionMethod); err != nil {
			log.Printf("[CrossServiceResolver] RESOLVES_TO write failed (%s → %s): %v", hcId, fId, err)
			continue
		}
		written++
	}
	return written, nil
}

// writeResolvesToEdge merges a RESOLVES_TO edge between callSiteId and handlerFuncId.
func (r *CrossServiceHandlerResolver) writeResolvesToEdge(
	ctx context.Context,
	callSiteId, handlerFuncId string,
	confidence float64, resolutionMethod string,
) error {
	now := time.Now().UTC().Unix()
	_, err := r.client.MergeRelationship(
		ctx,
		callSiteId, handlerFuncId,
		string(models.ResolvesToRel),
		map[string]any{},
		map[string]any{
			"confidence":       confidence,
			"resolutionMethod": resolutionMethod,
			"resolvedAt":       now,
		},
	)
	return err
}

// extractMethodName returns the last segment of a dot-separated method path.
// "PaymentService.CreatePayment" → "CreatePayment"
// "CreatePayment" → "CreatePayment"
func extractMethodName(targetMethod string) string {
	parts := strings.Split(targetMethod, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// extractURLPath strips scheme+host from a URL, returning just the path component.
// "https://payments.internal/v1/charge" → "/v1/charge"
func extractURLPath(rawURL string) string {
	for _, prefix := range []string{"https://", "http://", "grpc://"} {
		if strings.HasPrefix(rawURL, prefix) {
			rawURL = rawURL[len(prefix):]
			break
		}
	}
	// rawURL is now "host/path" or just "path" — skip to the first '/'
	if idx := strings.Index(rawURL, "/"); idx >= 0 {
		return rawURL[idx:]
	}
	// No slash: treat the whole string as a path segment (e.g. relative URLs)
	return "/" + rawURL
}

func scoreGRPCMatch(candidates int) (confidence float64, method string) {
	if candidates == 1 {
		return 0.9, "proto_matched"
	}
	return 0.6, "heuristic"
}

func scoreHTTPMatch(candidates int) (confidence float64, method string) {
	if candidates == 1 {
		return 0.85, "http_route_matched"
	}
	return 0.6, "heuristic"
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
