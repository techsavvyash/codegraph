package static

import (
	"context"
	"fmt"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	models "github.com/context-maximiser/code-graph/internal/model"
)

// SemanticEdgeDetector analyses the indexed SCIP symbol graph to detect
// higher-level semantic relationships such as message-broker consumers
// and scheduled/cron functions. Detected patterns are recorded either as
// node properties or as new relationship edges in Neo4j.
type SemanticEdgeDetector struct {
	client   *neo4j.Client
	scopeCtx models.ScopeContext
}

// NewSemanticEdgeDetector creates a SemanticEdgeDetector backed by the
// given Neo4j client. Call SetScope before running detection if you need
// a non-default scope.
func NewSemanticEdgeDetector(client *neo4j.Client) *SemanticEdgeDetector {
	return &SemanticEdgeDetector{
		client:   client,
		scopeCtx: models.DefaultScope(),
	}
}

// SetScope sets the scope context used to filter and tag detected edges.
func (sed *SemanticEdgeDetector) SetScope(scope models.ScopeContext) {
	sed.scopeCtx = scope
}

// brokerPatterns lists sub-strings that, when present in a SCIP Symbol's
// canonical name, indicate the function is interacting with a message broker.
var brokerPatterns = []string{
	"kafka-go",
	"sarama",
	"confluent-kafka",
	"amqp",
	"nats",
	"go-nsq",
	"segmentio/kafka",
}

// schedulerPatterns lists sub-strings that indicate a function is
// registering or being invoked as a scheduled/cron task.
var schedulerPatterns = []string{
	"robfig/cron",
	"gocron",
	"go-quartz",
	"clockwork",
}

// DetectSemanticEdges runs all semantic edge detectors and logs the
// results. Individual detector failures are collected but do not prevent
// subsequent detectors from running. An error is returned only if every
// detector fails.
func (sed *SemanticEdgeDetector) DetectSemanticEdges(ctx context.Context) error {
	var errs []error

	consumers, err := sed.DetectMessageConsumers(ctx)
	if err != nil {
		fmt.Printf("  Warning: message consumer detection failed: %v\n", err)
		errs = append(errs, err)
	} else {
		fmt.Printf("  Enriched %d functions as message-broker consumers\n", consumers)
	}

	scheduled, err := sed.DetectScheduledFunctions(ctx)
	if err != nil {
		fmt.Printf("  Warning: scheduled function detection failed: %v\n", err)
		errs = append(errs, err)
	} else {
		fmt.Printf("  Enriched %d functions as scheduled/cron tasks\n", scheduled)
	}

	if len(errs) == 2 {
		return fmt.Errorf("all semantic edge detectors failed: %v", errs)
	}
	return nil
}

// DetectMessageConsumers finds Function/Method nodes whose containing
// file also contains Reference nodes (within the function's line range)
// that point to Symbol nodes matching known message-broker library
// patterns. Matching functions are enriched with a consumesBroker=true
// property.
//
// Returns the number of functions enriched and any query error.
func (sed *SemanticEdgeDetector) DetectMessageConsumers(ctx context.Context) (int, error) {
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		MATCH (fn)<-[:CONTAINS]-(file:File)-[:CONTAINS]->(ref:Reference)-[:REFERENCES]->(sym:Symbol)
		WHERE ref.startLine >= fn.startLine AND ref.startLine <= fn.endLine
		  AND any(pattern IN $brokerPatterns WHERE sym.symbol CONTAINS pattern)
		WITH DISTINCT fn
		SET fn.consumesBroker = true
		RETURN count(fn) AS enriched
	`

	params := map[string]any{
		"scopeId":        sed.scopeCtx.ScopeID,
		"brokerPatterns": brokerPatterns,
	}

	records, err := sed.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return 0, fmt.Errorf("message consumer query failed: %w", err)
	}

	if len(records) == 0 {
		return 0, nil
	}

	enriched, _ := records[0].Get("enriched")
	if enriched == nil {
		return 0, nil
	}

	count, ok := enriched.(int64)
	if !ok {
		return 0, nil
	}

	return int(count), nil
}

// DetectScheduledFunctions finds Function/Method nodes whose containing
// file also contains Reference nodes (within the function's line range)
// that point to Symbol nodes matching known scheduler/cron library
// patterns. Matching functions are enriched with a scheduledTask=true
// property.
//
// Returns the number of functions enriched and any query error.
func (sed *SemanticEdgeDetector) DetectScheduledFunctions(ctx context.Context) (int, error) {
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		MATCH (fn)<-[:CONTAINS]-(file:File)-[:CONTAINS]->(ref:Reference)-[:REFERENCES]->(sym:Symbol)
		WHERE ref.startLine >= fn.startLine AND ref.startLine <= fn.endLine
		  AND any(pattern IN $schedulerPatterns WHERE sym.symbol CONTAINS pattern)
		WITH DISTINCT fn
		SET fn.scheduledTask = true
		RETURN count(fn) AS enriched
	`

	params := map[string]any{
		"scopeId":           sed.scopeCtx.ScopeID,
		"schedulerPatterns": schedulerPatterns,
	}

	records, err := sed.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return 0, fmt.Errorf("scheduled function query failed: %w", err)
	}

	if len(records) == 0 {
		return 0, nil
	}

	enriched, _ := records[0].Get("enriched")
	if enriched == nil {
		return 0, nil
	}

	count, ok := enriched.(int64)
	if !ok {
		return 0, nil
	}

	return int(count), nil
}
