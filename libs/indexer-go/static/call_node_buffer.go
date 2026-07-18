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

// bufferedBothNodeKeyEdge links two buffered nodes, both matched by nodeKey (within scope).
type bufferedBothNodeKeyEdge struct {
	fromNodeKey string
	toNodeKey   string
	props       map[string]any
}

// callNodeBuffer batches call-node writes and their relationships across detector passes.
type callNodeBuffer struct {
	scopeID string

	grpcCalls        map[string]map[string]any // nodeKey -> set props
	httpCalls        map[string]map[string]any
	outboxCalls      map[string]map[string]any
	eventTypes       map[string]map[string]any // nodeKey -> set props (shared channel hubs)
	dbCalls          map[string]map[string]any
	cacheCalls       map[string]map[string]any // nodeKey -> set props (Redis/cache call sites)
	externalCalls    map[string]map[string]any // nodeKey -> set props (service-scoped)
	externalServices map[string]map[string]any // nodeKey -> set props (shared hub)

	callsAPI    map[string]bufferedNodeEdge        // fromID + toNodeKey
	callsDB     map[string]bufferedNodeEdge        // fromID + toNodeKey
	callsCache  map[string]bufferedNodeEdge        // fromID + toNodeKey
	callsSvc    map[string]bufferedServiceEdge     // fromNodeKey + toID
	emitsEvent  map[string]bufferedBothNodeKeyEdge // OutboxCall nodeKey → EventType nodeKey
	usesService map[string]bufferedBothNodeKeyEdge // ExternalCall nodeKey → ExternalService nodeKey
}

func newCallNodeBuffer(scopeID string) *callNodeBuffer {
	return &callNodeBuffer{
		scopeID:          scopeID,
		grpcCalls:        make(map[string]map[string]any),
		httpCalls:        make(map[string]map[string]any),
		outboxCalls:      make(map[string]map[string]any),
		eventTypes:       make(map[string]map[string]any),
		dbCalls:          make(map[string]map[string]any),
		cacheCalls:       make(map[string]map[string]any),
		externalCalls:    make(map[string]map[string]any),
		externalServices: make(map[string]map[string]any),
		callsAPI:         make(map[string]bufferedNodeEdge),
		callsDB:          make(map[string]bufferedNodeEdge),
		callsCache:       make(map[string]bufferedNodeEdge),
		callsSvc:         make(map[string]bufferedServiceEdge),
		emitsEvent:       make(map[string]bufferedBothNodeKeyEdge),
		usesService:      make(map[string]bufferedBothNodeKeyEdge),
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

func (b *callNodeBuffer) addEventTypeNode(nodeKey string, props map[string]any) {
	b.addNode(b.eventTypes, nodeKey, props)
}

// addEmitsEventEdge buffers an EMITS_EVENT edge from an OutboxCall node to an EventType hub.
func (b *callNodeBuffer) addEmitsEventEdge(fromNodeKey, toNodeKey string, props map[string]any) {
	if b == nil || fromNodeKey == "" || toNodeKey == "" {
		return
	}
	key := fromNodeKey + "->" + toNodeKey
	b.emitsEvent[key] = bufferedBothNodeKeyEdge{
		fromNodeKey: fromNodeKey,
		toNodeKey:   toNodeKey,
		props:       maps.Clone(props),
	}
}

func (b *callNodeBuffer) addDBCall(nodeKey string, props map[string]any) {
	b.addNode(b.dbCalls, nodeKey, props)
}

func (b *callNodeBuffer) addCacheCall(nodeKey string, props map[string]any) {
	b.addNode(b.cacheCalls, nodeKey, props)
}

func (b *callNodeBuffer) addExternalCall(nodeKey string, props map[string]any) {
	b.addNode(b.externalCalls, nodeKey, props)
}

func (b *callNodeBuffer) addExternalServiceNode(nodeKey string, props map[string]any) {
	b.addNode(b.externalServices, nodeKey, props)
}

// addUsesServiceEdge buffers a USES_SERVICE edge from an ExternalCall node to an ExternalService hub.
func (b *callNodeBuffer) addUsesServiceEdge(fromNodeKey, toNodeKey string, props map[string]any) {
	if b == nil || fromNodeKey == "" || toNodeKey == "" {
		return
	}
	key := fromNodeKey + "->" + toNodeKey
	b.usesService[key] = bufferedBothNodeKeyEdge{
		fromNodeKey: fromNodeKey,
		toNodeKey:   toNodeKey,
		props:       maps.Clone(props),
	}
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

func (b *callNodeBuffer) addCallsCacheEdge(fromID, toNodeKey string, props map[string]any) {
	if b == nil || fromID == "" || toNodeKey == "" {
		return
	}
	key := fromID + "->" + toNodeKey
	b.callsCache[key] = bufferedNodeEdge{
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
	if err := b.flushNodes(ctx, client, "EventType", b.eventTypes); err != nil {
		return err
	}
	if err := b.flushNodes(ctx, client, "DBCall", b.dbCalls); err != nil {
		return err
	}
	if err := b.flushNodes(ctx, client, "CacheCall", b.cacheCalls); err != nil {
		return err
	}
	// ExternalCall and ExternalService nodes must be flushed BEFORE the CALLS_API
	// edge flush below — flushRelsByTargetNodeKey matches the target by nodeKey, so
	// the node must already exist when the edge MERGE runs.
	if err := b.flushNodes(ctx, client, "ExternalCall", b.externalCalls); err != nil {
		return err
	}
	if err := b.flushNodes(ctx, client, "ExternalService", b.externalServices); err != nil {
		return err
	}
	if err := b.flushRelsByTargetNodeKey(ctx, client, models.CallsAPIRel, b.callsAPI); err != nil {
		return err
	}
	if err := b.flushRelsByTargetNodeKey(ctx, client, models.CallsDBRel, b.callsDB); err != nil {
		return err
	}
	if err := b.flushRelsByTargetNodeKey(ctx, client, models.CallsCacheRel, b.callsCache); err != nil {
		return err
	}
	if err := b.flushCallsServiceRels(ctx, client, b.callsSvc); err != nil {
		return err
	}
	if err := b.flushRelsByBothNodeKeys(ctx, client, models.EmitsEventRel, b.emitsEvent); err != nil {
		return err
	}
	if err := b.flushRelsByBothNodeKeys(ctx, client, models.UsesServiceRel, b.usesService); err != nil {
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

// flushRelsByBothNodeKeys merges relationships whose endpoints are BOTH matched by nodeKey
// (within scope) — used for OutboxCall→EventType EMITS_EVENT edges.
func (b *callNodeBuffer) flushRelsByBothNodeKeys(
	ctx context.Context,
	client *neo4j.Client,
	relType models.RelationshipType,
	rels map[string]bufferedBothNodeKeyEdge,
) error {
	if len(rels) == 0 {
		return nil
	}

	batch := make([]map[string]any, 0, len(rels))
	for _, rel := range rels {
		batch = append(batch, map[string]any{
			"fromNodeKey": rel.fromNodeKey,
			"toNodeKey":   rel.toNodeKey,
			"props":       rel.props,
		})
	}

	cypher := fmt.Sprintf(`
		UNWIND $batch AS item
		MATCH (a {nodeKey: item.fromNodeKey, scopeId: $scopeId}), (b {nodeKey: item.toNodeKey, scopeId: $scopeId})
		MERGE (a)-[r:%s]->(b)
		SET r += item.props
	`, relType)

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
	b.eventTypes = make(map[string]map[string]any)
	b.dbCalls = make(map[string]map[string]any)
	b.cacheCalls = make(map[string]map[string]any)
	b.externalCalls = make(map[string]map[string]any)
	b.externalServices = make(map[string]map[string]any)
	b.callsAPI = make(map[string]bufferedNodeEdge)
	b.callsDB = make(map[string]bufferedNodeEdge)
	b.callsCache = make(map[string]bufferedNodeEdge)
	b.callsSvc = make(map[string]bufferedServiceEdge)
	b.emitsEvent = make(map[string]bufferedBothNodeKeyEdge)
	b.usesService = make(map[string]bufferedBothNodeKeyEdge)
}
