package static

import (
	"context"
	"fmt"
	"maps"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

type bufferedNodeEdge struct {
	fromID    string
	toNodeKey string
	props     map[string]any
}

type bufferedServiceEdge struct {
	fromNodeKey string
	toID        string
	props       map[string]any
}

// callNodeBuffer batches call-node writes and their relationships across detector passes.
type callNodeBuffer struct {
	scopeID string

	grpcCalls   map[string]map[string]any // nodeKey -> set props
	httpCalls   map[string]map[string]any
	outboxCalls map[string]map[string]any
	dbCalls     map[string]map[string]any

	callsAPI map[string]bufferedNodeEdge    // fromID + toNodeKey
	callsDB  map[string]bufferedNodeEdge    // fromID + toNodeKey
	callsSvc map[string]bufferedServiceEdge // fromNodeKey + toID
}

func newCallNodeBuffer(scopeID string) *callNodeBuffer {
	return &callNodeBuffer{
		scopeID:     scopeID,
		grpcCalls:   make(map[string]map[string]any),
		httpCalls:   make(map[string]map[string]any),
		outboxCalls: make(map[string]map[string]any),
		dbCalls:     make(map[string]map[string]any),
		callsAPI:    make(map[string]bufferedNodeEdge),
		callsDB:     make(map[string]bufferedNodeEdge),
		callsSvc:    make(map[string]bufferedServiceEdge),
	}
}

func (b *callNodeBuffer) addGRPCCall(nodeKey string, props map[string]any) {
	b.addNode(b.grpcCalls, nodeKey, props)
}

func (b *callNodeBuffer) addHTTPCall(nodeKey string, props map[string]any) {
	b.addNode(b.httpCalls, nodeKey, props)
}

func (b *callNodeBuffer) addOutboxCall(nodeKey string, props map[string]any) {
	b.addNode(b.outboxCalls, nodeKey, props)
}

func (b *callNodeBuffer) addDBCall(nodeKey string, props map[string]any) {
	b.addNode(b.dbCalls, nodeKey, props)
}

func (b *callNodeBuffer) addNode(target map[string]map[string]any, nodeKey string, props map[string]any) {
	if b == nil || nodeKey == "" {
		return
	}
	target[nodeKey] = maps.Clone(props)
}

func (b *callNodeBuffer) addCallsAPIEdge(fromID, toNodeKey string, props map[string]any) {
	if b == nil || fromID == "" || toNodeKey == "" {
		return
	}
	key := fromID + "->" + toNodeKey
	b.callsAPI[key] = bufferedNodeEdge{
		fromID:    fromID,
		toNodeKey: toNodeKey,
		props:     maps.Clone(props),
	}
}

func (b *callNodeBuffer) addCallsDBEdge(fromID, toNodeKey string, props map[string]any) {
	if b == nil || fromID == "" || toNodeKey == "" {
		return
	}
	key := fromID + "->" + toNodeKey
	b.callsDB[key] = bufferedNodeEdge{
		fromID:    fromID,
		toNodeKey: toNodeKey,
		props:     maps.Clone(props),
	}
}

func (b *callNodeBuffer) addCallsServiceEdge(fromNodeKey, toID string, props map[string]any) {
	if b == nil || fromNodeKey == "" || toID == "" {
		return
	}
	key := fromNodeKey + "->" + toID
	b.callsSvc[key] = bufferedServiceEdge{
		fromNodeKey: fromNodeKey,
		toID:        toID,
		props:       maps.Clone(props),
	}
}

func (b *callNodeBuffer) flush(ctx context.Context, client *neo4j.Client) error {
	if b == nil || client == nil {
		return nil
	}

	if err := b.flushNodes(ctx, client, "GRPCCall", b.grpcCalls); err != nil {
		return err
	}
	if err := b.flushNodes(ctx, client, "HTTPCall", b.httpCalls); err != nil {
		return err
	}
	if err := b.flushNodes(ctx, client, "OutboxCall", b.outboxCalls); err != nil {
		return err
	}
	if err := b.flushNodes(ctx, client, "DBCall", b.dbCalls); err != nil {
		return err
	}
	if err := b.flushRelsByTargetNodeKey(ctx, client, models.CallsAPIRel, b.callsAPI); err != nil {
		return err
	}
	if err := b.flushRelsByTargetNodeKey(ctx, client, models.CallsDBRel, b.callsDB); err != nil {
		return err
	}
	if err := b.flushCallsServiceRels(ctx, client, b.callsSvc); err != nil {
		return err
	}

	b.reset()
	return nil
}

func (b *callNodeBuffer) flushNodes(ctx context.Context, client *neo4j.Client, label string, nodes map[string]map[string]any) error {
	if len(nodes) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(nodes))
	for nodeKey, props := range nodes {
		items = append(items, map[string]any{
			"nodeKey": nodeKey,
			"scopeId": b.scopeID,
			"props":   props,
		})
	}
	if _, err := client.MergeNodesBatch(ctx, label, items, batchSize); err != nil {
		return fmt.Errorf("merge %s batch: %w", label, err)
	}
	return nil
}

func (b *callNodeBuffer) flushRelsByTargetNodeKey(
	ctx context.Context,
	client *neo4j.Client,
	relType models.RelationshipType,
	rels map[string]bufferedNodeEdge,
) error {
	if len(rels) == 0 {
		return nil
	}

	batch := make([]map[string]any, 0, len(rels))
	for _, rel := range rels {
		batch = append(batch, map[string]any{
			"fromId":    rel.fromID,
			"toNodeKey": rel.toNodeKey,
			"props":     rel.props,
		})
	}

	cypher := fmt.Sprintf(`
		UNWIND $batch AS item
		MATCH (a), (b {nodeKey: item.toNodeKey, scopeId: $scopeId})
		WHERE elementId(a) = item.fromId
		MERGE (a)-[r:%s]->(b)
		SET r += item.props
	`, relType)

	return executeBatchedQuery(ctx, client, cypher, batch, b.scopeID)
}

func (b *callNodeBuffer) flushCallsServiceRels(
	ctx context.Context,
	client *neo4j.Client,
	rels map[string]bufferedServiceEdge,
) error {
	if len(rels) == 0 {
		return nil
	}

	batch := make([]map[string]any, 0, len(rels))
	for _, rel := range rels {
		batch = append(batch, map[string]any{
			"fromNodeKey": rel.fromNodeKey,
			"toId":        rel.toID,
			"props":       rel.props,
		})
	}

	cypher := `
		UNWIND $batch AS item
		MATCH (a {nodeKey: item.fromNodeKey, scopeId: $scopeId}), (b)
		WHERE elementId(b) = item.toId
		MERGE (a)-[r:CALLS_SERVICE]->(b)
		SET r += item.props
	`

	return executeBatchedQuery(ctx, client, cypher, batch, b.scopeID)
}

func executeBatchedQuery(ctx context.Context, client *neo4j.Client, cypher string, items []map[string]any, scopeID string) error {
	if len(items) == 0 {
		return nil
	}
	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]
		_, err := client.ExecuteQuery(ctx, cypher, map[string]any{
			"scopeId": scopeID,
			"batch":   chunk,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *callNodeBuffer) reset() {
	b.grpcCalls = make(map[string]map[string]any)
	b.httpCalls = make(map[string]map[string]any)
	b.outboxCalls = make(map[string]map[string]any)
	b.dbCalls = make(map[string]map[string]any)
	b.callsAPI = make(map[string]bufferedNodeEdge)
	b.callsDB = make(map[string]bufferedNodeEdge)
	b.callsSvc = make(map[string]bufferedServiceEdge)
}
