package contracts

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

var (
	reProtoPackage = regexp.MustCompile(`^\s*option\s+go_package\s*=\s*"[^"]*[/;]([^"]+)"`)
	reProtoSyntax  = regexp.MustCompile(`^\s*package\s+(\S+)\s*;`)
	reService      = regexp.MustCompile(`^\s*service\s+(\w+)\s*\{?`)
	reRPC          = regexp.MustCompile(`^\s*rpc\s+(\w+)\s*\((\w+)\)\s+returns\s*\((\w+)\)`)
	rePbGoPackage  = regexp.MustCompile(`^\s*package\s+(\w+)\s*$`)
)

// ProtoIndexer parses .proto and .pb.go files to create ProtoContract and ProtoMethod
// nodes in Neo4j. These nodes act as the rendezvous between callers and handlers
// indexed in separate service runs.
type ProtoIndexer struct {
	client      *neo4j.Client
	serviceName string // --service flag value (e.g. "fx")
	scopeCtx    models.ScopeContext
}

// NewProtoIndexer creates a proto indexer for the given service.
func NewProtoIndexer(client *neo4j.Client, serviceName string, scopeCtx models.ScopeContext) *ProtoIndexer {
	return &ProtoIndexer{
		client:      client,
		serviceName: serviceName,
		scopeCtx:    scopeCtx,
	}
}

// IndexProtoFiles walks the given root directory and indexes all .proto files.
func (pi *ProtoIndexer) IndexProtoFiles(ctx context.Context, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if strings.HasSuffix(path, ".proto") {
			if indexErr := pi.indexProtoFile(ctx, path, root); indexErr != nil {
				log.Printf("[ProtoIndexer] %s: %v", path, indexErr)
			}
		}
		return nil
	})
}

// IndexPbGoFiles walks the root and extracts package names from .pb.go files.
// This is a lightweight pass used when .proto files aren't available.
func (pi *ProtoIndexer) IndexPbGoFiles(ctx context.Context, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if strings.HasSuffix(path, ".pb.go") {
			if indexErr := pi.indexPbGoFile(ctx, path, root); indexErr != nil {
				log.Printf("[ProtoIndexer] %s: %v", path, indexErr)
			}
		}
		return nil
	})
}

func (pi *ProtoIndexer) indexProtoFile(ctx context.Context, path, root string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	relPath, _ := filepath.Rel(root, path)

	var protoPackage, goPackage string
	var currentService string
	var services []struct {
		Name    string
		Methods []struct{ Name, Req, Resp string }
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		if m := reProtoSyntax.FindStringSubmatch(line); m != nil {
			protoPackage = m[1]
		}
		if m := reProtoPackage.FindStringSubmatch(line); m != nil {
			goPackage = m[1]
		}
		if m := reService.FindStringSubmatch(line); m != nil {
			currentService = m[1]
			services = append(services, struct {
				Name    string
				Methods []struct{ Name, Req, Resp string }
			}{Name: currentService})
		}
		if currentService != "" {
			if m := reRPC.FindStringSubmatch(line); m != nil {
				idx := len(services) - 1
				services[idx].Methods = append(services[idx].Methods, struct{ Name, Req, Resp string }{
					Name: m[1], Req: m[2], Resp: m[3],
				})
			}
			if strings.Contains(line, "}") {
				currentService = ""
			}
		}
	}

	pkg := goPackage
	if pkg == "" {
		pkg = protoPackage
	}

	for _, svc := range services {
		if err := pi.writeProtoContract(ctx, pkg, svc.Name, relPath); err != nil {
			return err
		}
		for _, method := range svc.Methods {
			if err := pi.writeProtoMethod(ctx, pkg, svc.Name, method.Name, method.Req, method.Resp); err != nil {
				log.Printf("[ProtoIndexer] method write failed: %v", err)
			}
		}
	}

	return nil
}

func (pi *ProtoIndexer) indexPbGoFile(ctx context.Context, path, root string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	relPath, _ := filepath.Rel(root, path)
	var pkg string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if m := rePbGoPackage.FindStringSubmatch(scanner.Text()); m != nil {
			pkg = m[1]
			break
		}
	}

	if pkg == "" {
		return nil
	}

	// For .pb.go files we only create a minimal ProtoContract (no methods).
	return pi.writeProtoContract(ctx, pkg, "", relPath)
}

func (pi *ProtoIndexer) writeProtoContract(ctx context.Context, protoPackage, protoService, protoFile string) error {
	if protoPackage == "" && protoService == "" {
		return nil
	}

	nodeKey := fmt.Sprintf("protocontract:%s:%s:%s", pi.serviceName, protoPackage, protoService)
	now := time.Now().UTC().Unix()

	mergeProps := map[string]any{"nodeKey": nodeKey, "scopeId": pi.scopeCtx.ScopeID}
	setProps := map[string]any{
		"nodeKey":      nodeKey,
		"protoPackage": protoPackage,
		"protoService": protoService,
		"protoFile":    protoFile,
		"serviceName":  pi.serviceName,
		"createdAt":    now,
		"updatedAt":    now,
	}
	for k, v := range pi.scopeCtx.Props() {
		setProps[k] = v
	}

	contractID, err := pi.client.MergeNode(ctx, []string{"ProtoContract"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("writeProtoContract: %w", err)
	}

	// Link to owning Service.
	svcCypher := `
		MATCH (s:Service {name: $name})
		WHERE s.scopeId = $scopeId OR s.scopeId = 'main'
		RETURN elementId(s) AS id LIMIT 1
	`
	rows, err := pi.client.ExecuteQuery(ctx, svcCypher, map[string]any{
		"name":    pi.serviceName,
		"scopeId": pi.scopeCtx.ScopeID,
	})
	if err != nil || len(rows) == 0 {
		return nil // Service not indexed yet — edge will be created on re-run
	}
	svcID, _ := rows[0].AsMap()["id"].(string)
	if svcID == "" {
		return nil
	}

	_, err = pi.client.MergeRelationship(ctx, contractID, svcID,
		string(models.BelongsToRel), map[string]any{}, map[string]any{})
	return err
}

func (pi *ProtoIndexer) writeProtoMethod(ctx context.Context, protoPackage, protoService, name, req, resp string) error {
	contractKey := fmt.Sprintf("protocontract:%s:%s:%s", pi.serviceName, protoPackage, protoService)
	methodKey := fmt.Sprintf("protomethod:%s:%s:%s:%s", pi.serviceName, protoPackage, protoService, name)
	now := time.Now().UTC().Unix()

	mergeProps := map[string]any{"nodeKey": methodKey, "scopeId": pi.scopeCtx.ScopeID}
	setProps := map[string]any{
		"nodeKey":      methodKey,
		"name":         name,
		"requestType":  req,
		"responseType": resp,
		"createdAt":    now,
		"updatedAt":    now,
	}
	for k, v := range pi.scopeCtx.Props() {
		setProps[k] = v
	}

	methodID, err := pi.client.MergeNode(ctx, []string{"ProtoMethod"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("writeProtoMethod: %w", err)
	}

	// Link contract → method.
	linkCypher := `
		MATCH (pc:ProtoContract {nodeKey: $contractKey}), (m:ProtoMethod)
		WHERE elementId(m) = $methodId
		MERGE (pc)-[:DEFINES_METHOD]->(m)
	`
	_, err = pi.client.ExecuteQuery(ctx, linkCypher, map[string]any{
		"contractKey": contractKey,
		"methodId":    methodID,
	})
	return err
}
