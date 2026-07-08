package models

// RelationshipType represents the different types of relationships in the graph
type RelationshipType string

const (
	// Structural Relationships
	ContainsRel   RelationshipType = "CONTAINS"
	DefinesRel    RelationshipType = "DEFINES"
	ReferencesRel RelationshipType = "REFERENCES"

	// Behavioral Relationships
	CallsRel      RelationshipType = "CALLS"
	FlowsToRel    RelationshipType = "FLOWS_TO"
	NextExecRel   RelationshipType = "NEXT_EXECUTION"

	// Object-Oriented Relationships
	// Deprecated: noise — not written during call-graph indexing; class inheritance is not in any RPC traversal
	InheritsFromRel RelationshipType = "INHERITS_FROM"
	ImplementsRel   RelationshipType = "IMPLEMENTS"

	// API Relationships
	CallsAPIRel RelationshipType = "CALLS_API"

	// Service Relationships
	DependsOnRel     RelationshipType = "DEPENDS_ON"
	CallsServiceRel  RelationshipType = "CALLS_SERVICE"
	// ResolvesToRel connects a GRPCCall/HTTPCall to the concrete handler Function in the
	// target service. Written by ResolveCrossServiceHandlersStage after all services are indexed.
	ResolvesToRel RelationshipType = "RESOLVES_TO"

	// Documentation Relationships
	DescribesRel RelationshipType = "DESCRIBES"
	MentionsRel  RelationshipType = "MENTIONS"

	// Async / Message Queue Relationships
	ConsumesFromRel RelationshipType = "CONSUMES_FROM"
	// EmitsEventRel connects an OutboxCall publish site to the EventType hub node it
	// broadcasts on. Written by the event detector during indexing. Carries destQueue,
	// destService and transport so a traversal knows the next hop without extra lookups.
	EmitsEventRel RelationshipType = "EMITS_EVENT"
	// RoutedToRel connects an EventType hub to the concrete listener/handler Function in
	// the consuming service. Written by ResolveCrossServiceHandlersStage after all
	// services are indexed. Carries service, tier ("entry"|"handler") and confidence.
	RoutedToRel RelationshipType = "ROUTED_TO"
	// Deprecated: noise — not written during call-graph indexing; cron trigger has no value in RPC context
	ScheduledByRel RelationshipType = "SCHEDULED_BY"

	// DB Relationships
	CallsDBRel RelationshipType = "CALLS_DB"

	// Control-flow reification relationships (Change #2)
	UnderControlFlowRel RelationshipType = "UNDER_CONTROL_FLOW"

	// Concurrent / transactional scope reification (Change #3)
	InParallelWithRel RelationshipType = "IN_PARALLEL_WITH"
	InTxRel           RelationshipType = "IN_TX"

	// TransitiveCallsAPIRel connects a caller Function to a GRPCCall/HTTPCall via a helper shim.
	// Written by HelperCollapseStage. Carries viaShim property naming the intermediate function.
	TransitiveCallsAPIRel RelationshipType = "TRANSITIVE_CALLS_API"

	// Proto contract relationships (Phase 1.4)
	DefinesMethodRel RelationshipType = "DEFINES_METHOD"  // ProtoContract → ProtoMethod
	ImplementedByRel RelationshipType = "IMPLEMENTED_BY"  // ProtoMethod → Function (handler)
	CalledByRel      RelationshipType = "CALLED_BY"       // ProtoMethod ← GRPCCall (caller)
	BelongsToRel     RelationshipType = "BELONGS_TO"      // ProtoContract → Service
)

// BaseRelationship represents common properties for all relationships
type BaseRelationship struct {
	ID         string            `json:"id,omitempty" neo4j:"id,omitempty"`
	Type       RelationshipType  `json:"type" neo4j:"type"`
	Properties map[string]any    `json:"properties,omitempty" neo4j:"properties,omitempty"`
	StartID    string            `json:"startId" neo4j:"startId"`
	EndID      string            `json:"endId" neo4j:"endId"`
	TenantID   string            `json:"tenantId,omitempty" neo4j:"tenantId,omitempty"`
	Repo       string            `json:"repo,omitempty" neo4j:"repo,omitempty"`
}

// ContainsRelationship represents hierarchical containment
type ContainsRelationship struct {
	BaseRelationship
	Order int `json:"order" neo4j:"order"` // Order within container
}

// DefinesRelationship represents symbol definitions
type DefinesRelationship struct {
	BaseRelationship
	IsExported bool `json:"isExported" neo4j:"isExported"`
}

// ReferencesRelationship represents symbol usage sites
type ReferencesRelationship struct {
	BaseRelationship
	IsDefinition bool `json:"isDefinition" neo4j:"isDefinition"`
	Line         int  `json:"line" neo4j:"line"`
	Column       int  `json:"column" neo4j:"column"`
}

// CallsRelationship represents function/method invocations.
//
// AST-derived enrichment fields (OrderIndex, LiteralArgs, NearestComment,
// ReceiverChain) let a consumer reconstruct execution order, distinguish
// otherwise-identical call sites by their string-literal args, surface the
// nearest source comment as "purpose" context, and resolve abstracted method
// chains (e.g. repository.Pgx().Payout.Save) back to the underlying receiver.
type CallsRelationship struct {
	BaseRelationship
	IsDynamic      bool     `json:"isDynamic" neo4j:"isDynamic"`
	Line           int      `json:"line" neo4j:"line"`
	IsRecursive    bool     `json:"isRecursive" neo4j:"isRecursive"`
	OrderIndex     int      `json:"orderIndex,omitempty" neo4j:"orderIndex,omitempty"`
	LiteralArgs    []string `json:"literalArgs,omitempty" neo4j:"literalArgs,omitempty"`
	NearestComment string   `json:"nearestComment,omitempty" neo4j:"nearestComment,omitempty"`
	ReceiverChain  []string `json:"receiverChain,omitempty" neo4j:"receiverChain,omitempty"`
}

// FlowsToRelationship represents data flow dependencies
type FlowsToRelationship struct {
	BaseRelationship
	Path     []string `json:"path" neo4j:"path"`
	FlowType string   `json:"flowType" neo4j:"flowType"` // direct, indirect, conditional
}

// NextExecutionRelationship represents control flow between statements
type NextExecutionRelationship struct {
	BaseRelationship
	IsConditional bool   `json:"isConditional" neo4j:"isConditional"`
	Condition     string `json:"condition" neo4j:"condition"`
}

// InheritsFromRelationship represents class inheritance
type InheritsFromRelationship struct {
	BaseRelationship
}

// ImplementsRelationship represents interface implementation or feature realization
type ImplementsRelationship struct {
	BaseRelationship
	Confidence       float64 `json:"confidence" neo4j:"confidence"`             // LLM validation confidence (0.0-1.0)
	ValidationMethod string  `json:"validationMethod" neo4j:"validationMethod"` // "llm", "heuristic", "hybrid"
	CodeSummary      string  `json:"codeSummary" neo4j:"codeSummary"`           // LLM-generated summary of implementing code
	SubgraphSize     int     `json:"subgraphSize" neo4j:"subgraphSize"`         // Number of functions in implementing subgraph
}

// CallsAPIRelationship represents API calls between services
type CallsAPIRelationship struct {
	BaseRelationship
	Timeout    int `json:"timeout" neo4j:"timeout"`       // Call timeout in milliseconds
	RetryCount int `json:"retryCount" neo4j:"retryCount"` // Number of retries
}

// DependsOnRelationship represents dependencies between services or modules
type DependsOnRelationship struct {
	BaseRelationship
	Version  string `json:"version" neo4j:"version"`
	IsDirect bool   `json:"isDirect" neo4j:"isDirect"`
}

// CallsServiceRelationship connects a GRPCCall/HTTPCall/OutboxCall node to a target Service node.
type CallsServiceRelationship struct {
	BaseRelationship
	Protocol string `json:"protocol" neo4j:"protocol"` // "grpc", "http", "outbox", "sqs", "kafka"
}

// DescribesRelationship connects documents to features or code elements
type DescribesRelationship struct {
	BaseRelationship
}

// MentionsRelationship represents references in documentation
type MentionsRelationship struct {
	BaseRelationship
	Context string `json:"context" neo4j:"context"`
}

// CallsDBRelationship connects a function to a DBCall node.
type CallsDBRelationship struct {
	BaseRelationship
	Line int `json:"line" neo4j:"line"`
}

// UnderControlFlowRelationship connects a CallEdge reified node to its enclosing ControlFlowScope.
type UnderControlFlowRelationship struct {
	BaseRelationship
	BranchDepth int `json:"branchDepth" neo4j:"branchDepth"`
}


// InParallelWithRelationship connects a CallEdge reified node to its enclosing ConcurrentScope.
type InParallelWithRelationship struct {
	BaseRelationship
	ForkPoint int `json:"forkPoint" neo4j:"forkPoint"` // line where the parallel scope opens
}

// InTxRelationship connects a CallEdge reified node to its enclosing TxScope.
type InTxRelationship struct {
	BaseRelationship
	Order int `json:"order" neo4j:"order"` // execution order within the tx
}

// TransitiveCallsAPIRelationship connects an outer Function to a call node through a helper shim.
type TransitiveCallsAPIRelationship struct {
	BaseRelationship
	ViaShim    string `json:"viaShim" neo4j:"viaShim"`
	ResolvedAt int64  `json:"resolvedAt" neo4j:"resolvedAt"`
}

// DefinesMethodRelationship connects a ProtoContract to its ProtoMethod.
type DefinesMethodRelationship struct {
	BaseRelationship
}

// ImplementedByRelationship connects a ProtoMethod to the concrete handler Function.
type ImplementedByRelationship struct {
	BaseRelationship
	Confidence float64 `json:"confidence" neo4j:"confidence"`
}

// CalledByRelationship connects a ProtoMethod to a GRPCCall site.
type CalledByRelationship struct {
	BaseRelationship
}

// BelongsToRelationship connects a ProtoContract to its owning Service.
type BelongsToRelationship struct {
	BaseRelationship
}

// ResolvesToRelationship connects a GRPCCall or HTTPCall node to the concrete handler
// Function/Method in the target service. Confidence reflects resolution certainty:
// ~0.9 for a unique name match, ~0.6 for a heuristic/multi-candidate selection.
type ResolvesToRelationship struct {
	BaseRelationship
	Confidence       float64 `json:"confidence" neo4j:"confidence"`
	ResolutionMethod string  `json:"resolutionMethod" neo4j:"resolutionMethod"` // "proto_matched", "http_route_matched", "heuristic"
}

// EmitsEventRelationship connects an OutboxCall publish site to the EventType hub it
// broadcasts on. DestService lets a traversal jump straight to the consuming service.
type EmitsEventRelationship struct {
	BaseRelationship
	Transport   string `json:"transport" neo4j:"transport"`     // "sqs", "kafka", "nats", "outbox"
	DestQueue   string `json:"destQueue" neo4j:"destQueue"`     // resolved queue/topic, e.g. "queue.event.event"
	DestService string `json:"destService" neo4j:"destService"` // owning service of destQueue, e.g. "event"
}

// RoutedToRelationship connects an EventType hub to the concrete listener/handler
// Function/Method in the consuming service. Tier is "entry" (the SQS listener entry,
// confidence 1.0) or "handler" (best-effort per-event handler, confidence ~0.8).
type RoutedToRelationship struct {
	BaseRelationship
	Service    string  `json:"service" neo4j:"service"`
	Tier       string  `json:"tier" neo4j:"tier"`
	Confidence float64 `json:"confidence" neo4j:"confidence"`
}

// RelationshipFactory creates relationships from type and properties
func RelationshipFactory(relType RelationshipType, startID, endID string, props map[string]any) interface{} {
	base := BaseRelationship{
		Type:       relType,
		Properties: props,
		StartID:    startID,
		EndID:      endID,
	}

	switch relType {
	case ContainsRel:
		return &ContainsRelationship{BaseRelationship: base}
	case DefinesRel:
		return &DefinesRelationship{BaseRelationship: base}
	case ReferencesRel:
		return &ReferencesRelationship{BaseRelationship: base}
	case CallsRel:
		return &CallsRelationship{BaseRelationship: base}
	case FlowsToRel:
		return &FlowsToRelationship{BaseRelationship: base}
	case NextExecRel:
		return &NextExecutionRelationship{BaseRelationship: base}
	case InheritsFromRel:
		return &InheritsFromRelationship{BaseRelationship: base}
	case ImplementsRel:
		return &ImplementsRelationship{BaseRelationship: base}
	case CallsAPIRel:
		return &CallsAPIRelationship{BaseRelationship: base}
	case CallsServiceRel:
		return &CallsServiceRelationship{BaseRelationship: base}
	case DependsOnRel:
		return &DependsOnRelationship{BaseRelationship: base}
	case DescribesRel:
		return &DescribesRelationship{BaseRelationship: base}
	case MentionsRel:
		return &MentionsRelationship{BaseRelationship: base}
	case CallsDBRel:
		return &CallsDBRelationship{BaseRelationship: base}
	case UnderControlFlowRel:
		return &UnderControlFlowRelationship{BaseRelationship: base}
	case InParallelWithRel:
		return &InParallelWithRelationship{BaseRelationship: base}
	case InTxRel:
		return &InTxRelationship{BaseRelationship: base}
	case ResolvesToRel:
		return &ResolvesToRelationship{BaseRelationship: base}
	case EmitsEventRel:
		return &EmitsEventRelationship{BaseRelationship: base}
	case RoutedToRel:
		return &RoutedToRelationship{BaseRelationship: base}
	case TransitiveCallsAPIRel:
		return &TransitiveCallsAPIRelationship{BaseRelationship: base}
	case DefinesMethodRel:
		return &DefinesMethodRelationship{BaseRelationship: base}
	case ImplementedByRel:
		return &ImplementedByRelationship{BaseRelationship: base}
	case CalledByRel:
		return &CalledByRelationship{BaseRelationship: base}
	case BelongsToRel:
		return &BelongsToRelationship{BaseRelationship: base}
	default:
		return &BaseRelationship{
			Type:       relType,
			Properties: props,
			StartID:    startID,
			EndID:      endID,
		}
	}
}