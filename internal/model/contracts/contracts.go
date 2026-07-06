package contracts

import (
	"context"
	"time"

	models "github.com/context-maximiser/code-graph/internal/model"
)

// RetrievalCandidate is the unified envelope for any retrieval result
// (graph, vector, text). All retrieval adapters must produce this.
type RetrievalCandidate struct {
	NodeKey  string         `json:"nodeKey"`
	NodeType string         `json:"nodeType"`
	Scope    string         `json:"scope"`
	ScopeID  string         `json:"scopeId"`
	Score    float64        `json:"score"`
	Source   string         `json:"source"` // "graph", "vector", "text"
	Metadata map[string]any `json:"metadata,omitempty"`
}

// InferenceResult is the output of any inference/scoring stage.
type InferenceResult struct {
	SourceKey    string        `json:"sourceKey"`
	TargetKey    string        `json:"targetKey"`
	RelationType string        `json:"relationType"`
	Confidence   float64       `json:"confidence"`
	Strategy     string        `json:"strategy"`
	Reasons      []string      `json:"reasons"`
	EvidenceRefs []EvidenceRef `json:"evidenceRefs"`
	CreatedAt    time.Time     `json:"createdAt"`
}

// EvidenceRef points to a specific piece of evidence backing an inference.
type EvidenceRef struct {
	Kind    string  `json:"kind"`              // "graph_edge", "vector_match", "text_match", "structural"
	NodeKey string  `json:"nodeKey,omitempty"`
	Detail  string  `json:"detail,omitempty"`
	Score   float64 `json:"score,omitempty"`
}

// ContextBundle is the bounded evidence bundle passed to generation.
type ContextBundle struct {
	Anchors    []RetrievalCandidate `json:"anchors"`
	Expansions []RetrievalCandidate `json:"expansions,omitempty"`
	Inferences []InferenceResult    `json:"inferences,omitempty"`
	Template   string               `json:"template"`
	MaxTokens  int                  `json:"maxTokens"`
	Scope      string               `json:"scope"`
	ScopeID    string               `json:"scopeId"`
}

// GenerationResult is the output of a generation stage with citations.
type GenerationResult struct {
	Content   string     `json:"content"`
	Citations []Citation `json:"citations"`
	Model     string     `json:"model"`
	Template  string     `json:"template"`
	CreatedAt time.Time  `json:"createdAt"`
}

// Citation links a generated statement to its evidence.
type Citation struct {
	StatementIndex int           `json:"statementIndex"`
	EvidenceRefs   []EvidenceRef `json:"evidenceRefs"`
}

// VerificationResult is the output of citation verification.
type VerificationResult struct {
	Passed            bool     `json:"passed"`
	TotalStatements   int      `json:"totalStatements"`
	CitedStatements   int      `json:"citedStatements"`
	UnsupportedClaims []int    `json:"unsupportedClaims,omitempty"`
	Errors            []string `json:"errors,omitempty"`
}

// Stage name constants.
const (
	StageRetrieval    = "retrieval"
	StageInference    = "inference"
	StageBundle       = "bundle"
	StageGeneration   = "generation"
	StageVerification = "verification"
)

// Retriever is the interface for any retrieval adapter.
type Retriever interface {
	Retrieve(ctx context.Context, query string, scope models.ScopeContext, limit int) ([]RetrievalCandidate, error)
}

// Inferrer scores and links candidates.
type Inferrer interface {
	Infer(ctx context.Context, candidates []RetrievalCandidate, scope models.ScopeContext) ([]InferenceResult, error)
}

// BundleBuilder assembles context bundles from anchors.
type BundleBuilder interface {
	Build(ctx context.Context, anchors []RetrievalCandidate, template string, scope models.ScopeContext) (*ContextBundle, error)
}

// Generator produces text with citations from a bundle.
type Generator interface {
	Generate(ctx context.Context, bundle *ContextBundle) (*GenerationResult, error)
}

// Verifier checks citation validity.
type Verifier interface {
	Verify(ctx context.Context, result *GenerationResult, scope models.ScopeContext) (*VerificationResult, error)
}
