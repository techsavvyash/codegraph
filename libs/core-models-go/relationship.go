package models

// RelationshipType represents the different types of relationships in the graph
type RelationshipType string

const (
	// Structural Relationships
	ContainsRel RelationshipType = "CONTAINS"
	DefinesRel  RelationshipType = "DEFINES"
	// Deprecated: noise — not written during call-graph indexing; volume too high and not in any RPC traversal query
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
	ExposesAPIRel RelationshipType = "EXPOSES_API"
	CallsAPIRel   RelationshipType = "CALLS_API"

	// Service Relationships
	DependsOnRel     RelationshipType = "DEPENDS_ON"
	CallsServiceRel  RelationshipType = "CALLS_SERVICE"

	// Documentation Relationships
	DescribesRel RelationshipType = "DESCRIBES"
	MentionsRel  RelationshipType = "MENTIONS"

	// Async / Message Queue Relationships
	ConsumesFromRel RelationshipType = "CONSUMES_FROM"
	// Deprecated: noise — not written during call-graph indexing; cron trigger has no value in RPC context
	ScheduledByRel RelationshipType = "SCHEDULED_BY"

	// DB Relationships
	CallsDBRel RelationshipType = "CALLS_DB"
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

// CallsRelationship represents function/method invocations
type CallsRelationship struct {
	BaseRelationship
	IsDynamic   bool `json:"isDynamic" neo4j:"isDynamic"`
	Line        int  `json:"line" neo4j:"line"`
	IsRecursive bool `json:"isRecursive" neo4j:"isRecursive"`
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

// ExposesAPIRelationship connects code handlers to API endpoints
type ExposesAPIRelationship struct {
	BaseRelationship
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
	case ExposesAPIRel:
		return &ExposesAPIRelationship{BaseRelationship: base}
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
	default:
		return &BaseRelationship{
			Type:       relType,
			Properties: props,
			StartID:    startID,
			EndID:      endID,
		}
	}
}