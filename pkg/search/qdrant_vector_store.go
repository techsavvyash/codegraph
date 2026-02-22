package search

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"

	pb "github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// QdrantVectorStore implements VectorStore using Qdrant's gRPC API.
type QdrantVectorStore struct {
	conn           *grpc.ClientConn
	pointsClient   pb.PointsClient
	collectClient  pb.CollectionsClient
}

// NewQdrantVectorStore creates a new Qdrant-backed vector store.
// host is the gRPC endpoint (e.g. "localhost:6334").
func NewQdrantVectorStore(host string) (*QdrantVectorStore, error) {
	conn, err := grpc.NewClient(host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Qdrant at %s: %w", host, err)
	}
	return &QdrantVectorStore{
		conn:          conn,
		pointsClient:  pb.NewPointsClient(conn),
		collectClient: pb.NewCollectionsClient(conn),
	}, nil
}

// Close closes the gRPC connection.
func (q *QdrantVectorStore) Close() error {
	if q.conn != nil {
		return q.conn.Close()
	}
	return nil
}

func (q *QdrantVectorStore) CreateIndex(ctx context.Context, name string, dimensions int, similarity string) error {
	dist := pb.Distance_Cosine
	switch similarity {
	case "euclidean":
		dist = pb.Distance_Euclid
	case "dot":
		dist = pb.Distance_Dot
	}

	_, err := q.collectClient.Create(ctx, &pb.CreateCollection{
		CollectionName: name,
		VectorsConfig: &pb.VectorsConfig{
			Config: &pb.VectorsConfig_Params{
				Params: &pb.VectorParams{
					Size:     uint64(dimensions),
					Distance: dist,
				},
			},
		},
	})
	if err != nil {
		// Collection may already exist – treat as success.
		log.Printf("Qdrant CreateIndex %s: %v (may already exist)", name, err)
		return nil
	}

	// Create payload indexes for efficient filtering.
	for _, field := range []string{"nodeLabel", "nodeKey"} {
		_, _ = q.pointsClient.CreateFieldIndex(ctx, &pb.CreateFieldIndexCollection{
			CollectionName: name,
			FieldName:      field,
			FieldType:      pb.FieldType_FieldTypeKeyword.Enum(),
		})
	}

	return nil
}

func (q *QdrantVectorStore) UpsertVectors(ctx context.Context, vectors []VectorUpsert) error {
	// Group by collection (derived from NodeLabel).
	byCollection := make(map[string][]*pb.PointStruct)

	for _, v := range vectors {
		collection := collectionForLabel(v.NodeLabel, len(v.Vector))

		payload := make(map[string]*pb.Value)
		payload["nodeKey"] = qdrantStrVal(v.ID)
		payload["nodeLabel"] = qdrantStrVal(v.NodeLabel)

		for k, val := range v.Metadata {
			if s, ok := val.(string); ok {
				payload[k] = qdrantStrVal(s)
			}
		}

		vec32 := toFloat32(v.Vector)

		point := &pb.PointStruct{
			Id: &pb.PointId{
				PointIdOptions: &pb.PointId_Uuid{Uuid: deterministicUUID(v.ID)},
			},
			Vectors: &pb.Vectors{
				VectorsOptions: &pb.Vectors_Vector{
					Vector: &pb.Vector{Data: vec32},
				},
			},
			Payload: payload,
		}

		byCollection[collection] = append(byCollection[collection], point)
	}

	for collection, points := range byCollection {
		_, err := q.pointsClient.Upsert(ctx, &pb.UpsertPoints{
			CollectionName: collection,
			Points:         points,
		})
		if err != nil {
			return fmt.Errorf("failed to upsert %d points into %s: %w", len(points), collection, err)
		}
	}

	return nil
}

func (q *QdrantVectorStore) Query(ctx context.Context, query VectorQuery) ([]VectorResult, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}

	// Determine which collections to search.
	collections := collectionsForQuery(query)

	var allResults []VectorResult
	for _, collection := range collections {
		var filter *pb.Filter
		if len(query.NodeLabels) > 0 {
			conditions := make([]*pb.Condition, 0, len(query.NodeLabels))
			for _, label := range query.NodeLabels {
				conditions = append(conditions, &pb.Condition{
					ConditionOneOf: &pb.Condition_Field{
						Field: &pb.FieldCondition{
							Key: "nodeLabel",
							Match: &pb.Match{
								MatchValue: &pb.Match_Keyword{Keyword: label},
							},
						},
					},
				})
			}
			filter = &pb.Filter{Should: conditions}
		}

		resp, err := q.pointsClient.Query(ctx, &pb.QueryPoints{
			CollectionName: collection,
			Query: &pb.Query{
				Variant: &pb.Query_Nearest{
					Nearest: &pb.VectorInput{
						Variant: &pb.VectorInput_Dense{
							Dense: &pb.DenseVector{Data: toFloat32(query.Vector)},
						},
					},
				},
			},
			Filter:      filter,
			Limit:       pb.PtrOf(uint64(limit)),
			WithPayload: &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
		})
		if err != nil {
			log.Printf("Warning: Qdrant query failed for collection %s: %v", collection, err)
			continue
		}

		for _, pt := range resp.GetResult() {
			metadata := make(map[string]any)
			for k, v := range pt.GetPayload() {
				if sv := v.GetStringValue(); sv != "" {
					metadata[k] = sv
				}
			}

			id := ""
			if nk, ok := metadata["nodeKey"].(string); ok {
				id = nk
			}

			allResults = append(allResults, VectorResult{
				ID:       id,
				Score:    float64(pt.GetScore()),
				Metadata: metadata,
			})
		}
	}

	if len(allResults) > limit {
		allResults = allResults[:limit]
	}
	return allResults, nil
}

func (q *QdrantVectorStore) DeleteVectors(ctx context.Context, ids []string) error {
	// We don't know which collection(s) the IDs belong to, so we derive UUIDs
	// and attempt deletion across common collections.
	uuids := make([]*pb.PointId, len(ids))
	for i, id := range ids {
		uuids[i] = &pb.PointId{
			PointIdOptions: &pb.PointId_Uuid{Uuid: deterministicUUID(id)},
		}
	}

	// Try to delete from common collections.
	for _, suffix := range []string{"function", "method", "class", "document", "feature"} {
		// We don't know the dimension; attempt both common sizes.
		for _, dim := range []int{768, 384} {
			collection := fmt.Sprintf("%s_embeddings_%d", suffix, dim)
			_, _ = q.pointsClient.Delete(ctx, &pb.DeletePoints{
				CollectionName: collection,
				Points: &pb.PointsSelector{
					PointsSelectorOneOf: &pb.PointsSelector_Points{
						Points: &pb.PointsIdsList{Ids: uuids},
					},
				},
			})
		}
	}

	return nil
}

// labelToIndexPrefix converts a Neo4j label to the index naming convention prefix.
func labelToIndexPrefix(label string) string {
	switch label {
	case "Function":
		return "function"
	case "Class":
		return "class"
	case "Method":
		return "method"
	case "Document":
		return "document"
	case "Feature":
		return "feature"
	case "DocumentChunk":
		return "docchunk"
	case "Symbol":
		return "symbol"
	default:
		return "function"
	}
}

// collectionForLabel maps a node label to a Qdrant collection name.
func collectionForLabel(label string, dimensions int) string {
	prefix := labelToIndexPrefix(label)
	return fmt.Sprintf("%s_embeddings_%d", prefix, dimensions)
}

// collectionsForQuery determines which Qdrant collections to search.
func collectionsForQuery(q VectorQuery) []string {
	dim := len(q.Vector)

	if q.IndexName != "" {
		return []string{q.IndexName}
	}

	if len(q.NodeLabels) > 0 {
		cols := make([]string, 0, len(q.NodeLabels))
		for _, label := range q.NodeLabels {
			cols = append(cols, collectionForLabel(label, dim))
		}
		return cols
	}

	// Default: search common collections. docchunk covers prose doc queries;
	// symbol covers CLI command vars, exported types, and other named definitions.
	return []string{
		fmt.Sprintf("function_embeddings_%d", dim),
		fmt.Sprintf("method_embeddings_%d", dim),
		fmt.Sprintf("class_embeddings_%d", dim),
		fmt.Sprintf("document_embeddings_%d", dim),
		fmt.Sprintf("feature_embeddings_%d", dim),
		fmt.Sprintf("docchunk_embeddings_%d", dim),
		fmt.Sprintf("symbol_embeddings_%d", dim),
	}
}

// deterministicUUID generates a deterministic UUID from a string key using SHA-256.
func deterministicUUID(key string) string {
	h := sha256.Sum256([]byte(key))
	// Format as UUID v4 (variant bits set).
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		h[0:4],
		h[4:6],
		(h[6]&0x0f)|0x40, // Version 4.
		(h[8]&0x3f)|0x80, // Variant.
		h[10:16],
	)
}

func qdrantStrVal(s string) *pb.Value {
	return &pb.Value{Kind: &pb.Value_StringValue{StringValue: s}}
}

func toFloat32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = float32(f)
	}
	return out
}

// ScrolledPoint is a lightweight point representation returned by ScrollPoints.
type ScrolledPoint struct {
	UUID    string            // Qdrant point UUID
	Payload map[string]string // String payload fields (nodeKey, nodeLabel, name, etc.)
}

// ScrollPoints returns all points in a collection without their vectors.
// It pages through results using Qdrant's scroll API.
func (q *QdrantVectorStore) ScrollPoints(ctx context.Context, collection string) ([]ScrolledPoint, error) {
	var all []ScrolledPoint
	var offset *pb.PointId // nil means start from beginning

	for {
		resp, err := q.pointsClient.Scroll(ctx, &pb.ScrollPoints{
			CollectionName: collection,
			Limit:          pb.PtrOf(uint32(250)),
			Offset:         offset,
			WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
			WithVectors:    &pb.WithVectorsSelector{SelectorOptions: &pb.WithVectorsSelector_Enable{Enable: false}},
		})
		if err != nil {
			return nil, fmt.Errorf("scroll failed for %s: %w", collection, err)
		}

		for _, pt := range resp.GetResult() {
			sp := ScrolledPoint{Payload: make(map[string]string)}
			if u, ok := pt.Id.GetPointIdOptions().(*pb.PointId_Uuid); ok {
				sp.UUID = u.Uuid
			}
			for k, v := range pt.GetPayload() {
				if sv := v.GetStringValue(); sv != "" {
					sp.Payload[k] = sv
				}
			}
			all = append(all, sp)
		}

		if resp.GetNextPageOffset() == nil {
			break
		}
		offset = resp.GetNextPageOffset()
	}

	return all, nil
}

// SetPayloadFields merges extra payload fields onto existing Qdrant points.
// pointUUIDs and payloads must be the same length.
func (q *QdrantVectorStore) SetPayloadFields(ctx context.Context, collection string, pointUUIDs []string, payloads []map[string]string) error {
	if len(pointUUIDs) == 0 {
		return nil
	}

	// Qdrant's SetPayload operates on one point at a time or with a filter.
	// Batch by sending up to 100 per RPC using OverwritePayload with point IDs.
	const batchSize = 100
	for i := 0; i < len(pointUUIDs); i += batchSize {
		end := i + batchSize
		if end > len(pointUUIDs) {
			end = len(pointUUIDs)
		}

		for j := i; j < end; j++ {
			payload := make(map[string]*pb.Value, len(payloads[j]))
			for k, v := range payloads[j] {
				payload[k] = qdrantStrVal(v)
			}
			_, err := q.pointsClient.SetPayload(ctx, &pb.SetPayloadPoints{
				CollectionName: collection,
				Payload:        payload,
				PointsSelector: &pb.PointsSelector{
					PointsSelectorOneOf: &pb.PointsSelector_Points{
						Points: &pb.PointsIdsList{
							Ids: []*pb.PointId{{
								PointIdOptions: &pb.PointId_Uuid{Uuid: pointUUIDs[j]},
							}},
						},
					},
				},
			})
			if err != nil {
				log.Printf("Warning: SetPayload failed for %s point %s: %v", collection, pointUUIDs[j], err)
			}
		}
	}
	return nil
}

// Compile-time interface check.
var _ VectorStore = (*QdrantVectorStore)(nil)
