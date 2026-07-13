package models

import (
	"time"
)

// NodeType represents the different types of nodes in the graph
type NodeType string

const (
	ServiceNode   NodeType = "Service"
	FileNode      NodeType = "File"
	ModuleNode    NodeType = "Module"
	ClassNode     NodeType = "Class"
	InterfaceNode NodeType = "Interface"
	FunctionNode  NodeType = "Function"
	MethodNode    NodeType = "Method"
	VariableNode  NodeType = "Variable"
	ParameterNode NodeType = "Parameter"
	SymbolNode    NodeType = "Symbol"
	GRPCCallNode   NodeType = "GRPCCall"
	HTTPCallNode   NodeType = "HTTPCall"
	OutboxCallNode     NodeType = "OutboxCall"
	EventTypeNode      NodeType = "EventType"
	DBCallNode         NodeType = "DBCall"
	ExternalCallNode   NodeType = "ExternalCall"
	ExternalServiceNode NodeType = "ExternalService"
	CommentNode          NodeType = "Comment"
	FlowNode             NodeType = "Flow"
	ControlFlowScopeNode NodeType = "ControlFlowScope"
	ConcurrentScopeNode  NodeType = "ConcurrentScope"
	TxScopeNode          NodeType = "TxScope"
	ProtoContractNode    NodeType = "ProtoContract"
	ProtoMethodNode      NodeType = "ProtoMethod"

	// Deprecated: noise — not written during call-graph indexing.
	// Kept for document-indexer and generated-context compatibility.
	DocumentNode             NodeType = "Document"
	DocumentChunkNode        NodeType = "DocumentChunk"
	FeatureNode              NodeType = "Feature"
	PullRequestNode          NodeType = "PullRequest"
	GeneratedDocNode         NodeType = "GeneratedDoc"
	GenerationDiagnosticNode NodeType = "GenerationDiagnostic"
)

// BaseNode represents common properties for all nodes
type BaseNode struct {
	ID        string            `json:"id,omitempty" neo4j:"id,omitempty"`
	NodeKey   string            `json:"nodeKey,omitempty" neo4j:"nodeKey,omitempty"`
	Labels    []string          `json:"labels,omitempty" neo4j:"labels,omitempty"`
	Props     map[string]any    `json:"properties,omitempty" neo4j:"properties,omitempty"`
	Scope     string            `json:"scope,omitempty" neo4j:"scope,omitempty"`
	ScopeID   string            `json:"scopeId,omitempty" neo4j:"scopeId,omitempty"`
	TenantID  string            `json:"tenantId,omitempty" neo4j:"tenantId,omitempty"`
	Repo      string            `json:"repo,omitempty" neo4j:"repo,omitempty"`
	RepoID    string            `json:"repoId,omitempty" neo4j:"repoId,omitempty"`
	CreatedAt time.Time         `json:"createdAt" neo4j:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt" neo4j:"updatedAt"`
}

// Service represents a microservice or application component
type Service struct {
	BaseNode
	Name          string `json:"name" neo4j:"name"`
	Language      string `json:"language" neo4j:"language"`
	Version       string `json:"version" neo4j:"version"`
	RepositoryURL string `json:"repositoryUrl" neo4j:"repositoryUrl"`
}

// File represents a source code file
type File struct {
	BaseNode
	Path         string `json:"path" neo4j:"path"`
	AbsolutePath string `json:"absolutePath" neo4j:"absolutePath"`
	Language     string `json:"language" neo4j:"language"`
	Size         int64  `json:"size" neo4j:"size"`
	LineCount    int    `json:"lineCount" neo4j:"lineCount"`
	Hash         string `json:"hash" neo4j:"hash"`
}

// Module represents a logical code grouping (package, namespace, module)
type Module struct {
	BaseNode
	Name       string `json:"name" neo4j:"name"`
	FQN        string `json:"fqn" neo4j:"fqn"`
	Type       string `json:"type" neo4j:"type"`
	IsExported bool   `json:"isExported" neo4j:"isExported"`
}

// Class represents an object-oriented class definition
type Class struct {
	BaseNode
	Name           string `json:"name" neo4j:"name"`
	FQN            string `json:"fqn" neo4j:"fqn"`
	FilePath       string `json:"filePath" neo4j:"filePath"`
	StartLine      int    `json:"startLine" neo4j:"startLine"`
	EndLine        int    `json:"endLine" neo4j:"endLine"`
	AccessModifier string `json:"accessModifier" neo4j:"accessModifier"`
	IsAbstract     bool   `json:"isAbstract" neo4j:"isAbstract"`
	IsInterface    bool   `json:"isInterface" neo4j:"isInterface"`
	Docstring      string `json:"docstring" neo4j:"docstring"`
}

// Interface represents an interface definition
type Interface struct {
	BaseNode
	Name      string `json:"name" neo4j:"name"`
	FQN       string `json:"fqn" neo4j:"fqn"`
	FilePath  string `json:"filePath" neo4j:"filePath"`
	StartLine int    `json:"startLine" neo4j:"startLine"`
	EndLine   int    `json:"endLine" neo4j:"endLine"`
	Docstring string `json:"docstring" neo4j:"docstring"`
}

// Function represents a standalone function or static method
type Function struct {
	BaseNode
	Name             string `json:"name" neo4j:"name"`
	Signature        string `json:"signature" neo4j:"signature"`
	ReturnType       string `json:"returnType" neo4j:"returnType"`
	FilePath         string `json:"filePath" neo4j:"filePath"`
	AbsoluteFilePath string `json:"absoluteFilePath" neo4j:"absoluteFilePath"`
	StartLine        int    `json:"startLine" neo4j:"startLine"`
	EndLine          int    `json:"endLine" neo4j:"endLine"`
	IsExported       bool   `json:"isExported" neo4j:"isExported"`
	IsAsync          bool   `json:"isAsync" neo4j:"isAsync"`
	Complexity       int    `json:"complexity" neo4j:"complexity"`
	Docstring        string `json:"docstring" neo4j:"docstring"`
	IsRPCHandler     bool   `json:"isRPCHandler" neo4j:"isRPCHandler"`
	// Summary is a one-line behavioural description generated deterministically at index
	// time from the name, signature, and docstring. Used by MCP navigation tools so an
	// LLM can decide relevance without opening the source file.
	Summary          string `json:"summary" neo4j:"summary"`
}

// Method represents an instance method belonging to a class
type Method struct {
	BaseNode
	Name             string `json:"name" neo4j:"name"`
	Signature        string `json:"signature" neo4j:"signature"`
	ReturnType       string `json:"returnType" neo4j:"returnType"`
	AccessModifier   string `json:"accessModifier" neo4j:"accessModifier"`
	FilePath         string `json:"filePath" neo4j:"filePath"`
	AbsoluteFilePath string `json:"absoluteFilePath" neo4j:"absoluteFilePath"`
	StartLine        int    `json:"startLine" neo4j:"startLine"`
	EndLine          int    `json:"endLine" neo4j:"endLine"`
	IsStatic         bool   `json:"isStatic" neo4j:"isStatic"`
	IsAbstract       bool   `json:"isAbstract" neo4j:"isAbstract"`
	IsOverride       bool   `json:"isOverride" neo4j:"isOverride"`
	Complexity       int    `json:"complexity" neo4j:"complexity"`
	Docstring        string `json:"docstring" neo4j:"docstring"`
	IsRPCHandler     bool   `json:"isRPCHandler" neo4j:"isRPCHandler"`
	// Summary is a one-line behavioural description generated deterministically at index
	// time from the name, signature, and docstring. Used by MCP navigation tools so an
	// LLM can decide relevance without opening the source file.
	Summary          string `json:"summary" neo4j:"summary"`
}

// Variable represents a variable declaration
type Variable struct {
	BaseNode
	Name         string `json:"name" neo4j:"name"`
	Type         string `json:"type" neo4j:"type"`
	Scope        string `json:"scope" neo4j:"scope"`
	FilePath     string `json:"filePath" neo4j:"filePath"`
	StartLine    int    `json:"startLine" neo4j:"startLine"`
	EndLine      int    `json:"endLine" neo4j:"endLine"`
	IsConstant   bool   `json:"isConstant" neo4j:"isConstant"`
	InitialValue string `json:"initialValue" neo4j:"initialValue"`
}

// Parameter represents a function/method parameter
type Parameter struct {
	BaseNode
	Name         string `json:"name" neo4j:"name"`
	Type         string `json:"type" neo4j:"type"`
	Index        int    `json:"index" neo4j:"index"`
	IsOptional   bool   `json:"isOptional" neo4j:"isOptional"`
	DefaultValue string `json:"defaultValue" neo4j:"defaultValue"`
}

// Symbol represents a canonical code symbol using SCIP format
type Symbol struct {
	BaseNode
	Symbol        string `json:"symbol" neo4j:"symbol"`
	Kind          string `json:"kind" neo4j:"kind"`
	DisplayName   string `json:"displayName" neo4j:"displayName"`
	Documentation string `json:"documentation" neo4j:"documentation"`
}

// GRPCCall represents a gRPC outbound call site within a function body.
type GRPCCall struct {
	BaseNode
	CallerService string `json:"callerService" neo4j:"callerService"`
	TargetService string `json:"targetService" neo4j:"targetService"`
	TargetMethod  string `json:"targetMethod"  neo4j:"targetMethod"`
	ProtoPackage  string `json:"protoPackage"  neo4j:"protoPackage"`
	ProtoService  string `json:"protoService"  neo4j:"protoService"`
	ProtoMethod   string `json:"protoMethod"   neo4j:"protoMethod"`
	FilePath      string `json:"filePath"      neo4j:"filePath"`
	Line          int    `json:"line"          neo4j:"line"`
}

// HTTPCall represents an outbound HTTP call site within a function body.
type HTTPCall struct {
	BaseNode
	CallerService string `json:"callerService" neo4j:"callerService"`
	TargetService string `json:"targetService" neo4j:"targetService"`
	URL           string `json:"url"           neo4j:"url"`
	Method        string `json:"method"        neo4j:"method"`
	FilePath      string `json:"filePath"      neo4j:"filePath"`
	Line          int    `json:"line"          neo4j:"line"`
}

// OutboxCall represents a message publish/enqueue site (outbox, SQS, Kafka, NATS, etc.).
//
// EventType now holds the SEMANTIC event name ("settlement.failed") rather than the queue
// env-var name, split into EventGroup/EventAction. DestQueue is the resolved queue string
// ("queue.event.event") and DestService its owning service ("event"). Dynamic is true when
// the action could not be resolved statically (e.g. "payout.*" from a runtime status var).
type OutboxCall struct {
	BaseNode
	CallerService string `json:"callerService"  neo4j:"callerService"`
	Transport     string `json:"transport"      neo4j:"transport"`
	EventType     string `json:"eventType"      neo4j:"eventType"`
	EventGroup    string `json:"eventGroup"     neo4j:"eventGroup"`
	EventAction   string `json:"eventAction"    neo4j:"eventAction"`
	QueueOrTopic  string `json:"queueOrTopic"   neo4j:"queueOrTopic"`
	DestQueue     string `json:"destQueue"      neo4j:"destQueue"`
	DestService   string `json:"destService"    neo4j:"destService"`
	Dynamic       bool   `json:"dynamic"        neo4j:"dynamic"`
	FilePath      string `json:"filePath"       neo4j:"filePath"`
	Line          int    `json:"line"           neo4j:"line"`
}

// EventType is the shared "channel" hub node for an async event name. Every producer that
// broadcasts "settlement.failed" and every listener tuned to it connect to the SAME node,
// making "who touches this event?" a one-hop query. Group-fallback hubs (action unresolved)
// use eventType "<group>.*" with Dynamic=true.
type EventType struct {
	BaseNode
	EventType string `json:"eventType" neo4j:"eventType"` // "settlement.failed" or "payout.*"
	Group     string `json:"group"     neo4j:"group"`     // "settlement"
	Action    string `json:"action"    neo4j:"action"`    // "failed" ("" for group-fallback)
	Dynamic   bool   `json:"dynamic"   neo4j:"dynamic"`   // true when action is a runtime value
}

// ExternalCall represents a single call site to an external managed service (e.g. AWS Cognito).
// Service-scoped (per file:line within a service), mirrors OutboxCall.
type ExternalCall struct {
	BaseNode
	Provider        string `json:"provider"          neo4j:"provider"`          // "aws"
	ExternalService string `json:"externalService"   neo4j:"externalService"`   // "cognito"
	Operation       string `json:"operation"         neo4j:"operation"`         // underlying SDK op, e.g. "InitiateAuth"
	Variant         string `json:"variant"           neo4j:"variant"`           // AuthFlow / "admin" / ""
	WrapperFunc     string `json:"wrapperFunc"       neo4j:"wrapperFunc"`       // wrapper fn actually called
	CallerService   string `json:"callerService"     neo4j:"callerService"`
	FilePath        string `json:"filePath"          neo4j:"filePath"`
	Line            int    `json:"line"              neo4j:"line"`
	Name            string `json:"name"              neo4j:"name"`              // caption: "account:cognito.InitiateAuth"
	FlowTag         string `json:"flowTag,omitempty" neo4j:"flowTag,omitempty"` // e.g. "mfa"
}

// ExternalService is the shared hub node for an external managed service provider.
// NOT service-scoped — one node per (provider, name) shared across all services.
type ExternalService struct {
	BaseNode
	Provider    string `json:"provider"    neo4j:"provider"`    // "aws"
	Name        string `json:"name"        neo4j:"name"`        // "cognito"
	Category    string `json:"category"    neo4j:"category"`    // "identity" | "messaging" | "email"
	DisplayName string `json:"displayName" neo4j:"displayName"` // "AWS Cognito"
}

// DBCall represents a database query call site within a function body.
// NodeKey pattern: dbcall:{scopeId}:{serviceName}:{filePath}:{line}
type DBCall struct {
	BaseNode
	Name                string `json:"name" neo4j:"name"`                               // method name, e.g. "GetByID", "Save"
	Table               string `json:"table" neo4j:"table"`
	Operation           string `json:"operation" neo4j:"operation"` // SELECT, INSERT, UPDATE, DELETE, UPSERT
	QueryPattern        string `json:"queryPattern" neo4j:"queryPattern"`
	ServiceName         string `json:"serviceName" neo4j:"serviceName"`
	FilePath            string `json:"filePath" neo4j:"filePath"`
	Line                int    `json:"line" neo4j:"line"`
	RepositoryInterface string `json:"repositoryInterface" neo4j:"repositoryInterface"` // e.g. "Account", "AccountMetadata"
	RepositoryMethod    string `json:"repositoryMethod" neo4j:"repositoryMethod"`       // e.g. "GetByID", "Save"
}

// Comment represents code comments and docstrings
type Comment struct {
	BaseNode
	Text        string `json:"text" neo4j:"text"`
	Type        string `json:"type" neo4j:"type"`
	FilePath    string `json:"filePath" neo4j:"filePath"`
	StartLine   int    `json:"startLine" neo4j:"startLine"`
	EndLine     int    `json:"endLine" neo4j:"endLine"`
	IsDocstring bool   `json:"isDocstring" neo4j:"isDocstring"`
}

// Document represents technical or business documents
type Document struct {
	BaseNode
	Title     string `json:"title" neo4j:"title"`
	Type      string `json:"type" neo4j:"type"`
	SourceURL string `json:"sourceUrl" neo4j:"sourceUrl"`
	Content   string `json:"content" neo4j:"content"`
}

// DocumentChunk represents a chunk of a document for granular linking and incremental updates
type DocumentChunk struct {
	BaseNode
	DocumentKey string `json:"documentKey" neo4j:"documentKey"`
	ChunkIndex  int    `json:"chunkIndex" neo4j:"chunkIndex"`
	HeadingPath string `json:"headingPath" neo4j:"headingPath"`
	Content     string `json:"content" neo4j:"content"`
	TextHash    string `json:"textHash" neo4j:"textHash"`
	StartOffset int    `json:"startOffset" neo4j:"startOffset"`
	EndOffset   int    `json:"endOffset" neo4j:"endOffset"`
}

// Flow represents a first-class execution flow (e.g., an API request path, consumer pipeline).
type Flow struct {
	BaseNode
	Name               string `json:"name" neo4j:"name"`
	EntrypointKey      string `json:"entrypointKey" neo4j:"entrypointKey"`
	FlowType           string `json:"flowType" neo4j:"flowType"` // "api", "consumer", "cron"
	MaxDepth           int    `json:"maxDepth" neo4j:"maxDepth"`
	// Spine cache (Change #7): pre-computed at index time so MCP consumers avoid re-traversal.
	BehavioralSummary  string `json:"behavioralSummary,omitempty" neo4j:"behavioralSummary"`
	SpineHash          string `json:"spineHash,omitempty" neo4j:"spineHash"`
}

// Feature represents a specific feature or capability
type Feature struct {
	BaseNode
	Name        string   `json:"name" neo4j:"name"`
	Description string   `json:"description" neo4j:"description"`
	Status      string   `json:"status" neo4j:"status"`
	Priority    string   `json:"priority" neo4j:"priority"`
	Tags        []string `json:"tags" neo4j:"tags"`
}

// PullRequest represents a pull request overlay in the graph.
type PullRequest struct {
	BaseNode
	PRID        string `json:"prId" neo4j:"prId"`
	Title       string `json:"title" neo4j:"title"`
	Author      string `json:"author" neo4j:"author"`
	BaseBranch  string `json:"baseBranch" neo4j:"baseBranch"`
	HeadBranch  string `json:"headBranch" neo4j:"headBranch"`
	Status      string `json:"status" neo4j:"status"` // open, merged, closed
	Description string `json:"description" neo4j:"description"`
}

// GeneratedDoc represents auto-generated documentation stored as a knowledge unit.
type GeneratedDoc struct {
	BaseNode
	Type       string `json:"type" neo4j:"type"`       // pr_summary, flow_summary, docstring_suggestion
	Title      string `json:"title" neo4j:"title"`
	Content    string `json:"content" neo4j:"content"`
	Model      string `json:"model" neo4j:"model"`           // LLM model used
	SourceType string `json:"sourceType" neo4j:"sourceType"` // What triggered generation
	SourceKey  string `json:"sourceKey" neo4j:"sourceKey"`   // nodeKey of the source
	Statements string `json:"statements" neo4j:"statements"` // JSON-serialized []Statement
	Citations  string `json:"citations" neo4j:"citations"`   // JSON-serialized []Citation (statement-indexed evidence refs)
}

// GenerationDiagnostic captures a rejected generation attempt for audit and debugging.
type GenerationDiagnostic struct {
	BaseNode
	Type              string   `json:"type" neo4j:"type"`                           // pr_summary, flow_summary, docstring_suggestion
	SourceType        string   `json:"sourceType" neo4j:"sourceType"`
	SourceKey         string   `json:"sourceKey" neo4j:"sourceKey"`
	Model             string   `json:"model" neo4j:"model"`
	RejectionReasons  []string `json:"rejectionReasons" neo4j:"rejectionReasons"`
	UnsupportedClaims []int    `json:"unsupportedClaims" neo4j:"unsupportedClaims"`
	Content           string   `json:"content" neo4j:"content"`                     // Draft content that failed verification
}

// ControlFlowScope represents a single conditional/loop block that encloses one or more calls.
type ControlFlowScope struct {
	BaseNode
	Kind           string `json:"kind" neo4j:"kind"`                     // if, else, switch, switch_case, for, range, select, select_case
	Condition      string `json:"condition" neo4j:"condition"`           // raw source text ≤200 chars
	FilePath       string `json:"filePath" neo4j:"filePath"`
	StartLine      int    `json:"startLine" neo4j:"startLine"`
	EndLine        int    `json:"endLine" neo4j:"endLine"`
	Depth          int    `json:"depth" neo4j:"depth"`                   // 1-based nesting level
	ParentScopeKey string `json:"parentScopeKey" neo4j:"parentScopeKey"` // nodeKey of enclosing scope, empty for top-level
}


// ConcurrentScope represents a `go func()`, `errgroup.Go` body, or similar concurrent
// dispatch site. All calls whose source position falls inside its range execute in
// parallel with the enclosing caller's normal sequence.
type ConcurrentScope struct {
	BaseNode
	Kind              string `json:"kind" neo4j:"kind"` // goroutine | errgroup | waitgroup | channel_send
	EnclosingFunction string `json:"enclosingFunction" neo4j:"enclosingFunction"`
	FilePath          string `json:"filePath" neo4j:"filePath"`
	StartLine         int    `json:"startLine" neo4j:"startLine"`
	EndLine           int    `json:"endLine" neo4j:"endLine"`
}

// TxScope represents a transaction boundary. Calls inside its range share the same
// transactional context.
type TxScope struct {
	BaseNode
	Kind              string `json:"kind" neo4j:"kind"` // pgx_begintx | with_tx_func | gorm_transaction
	EnclosingFunction string `json:"enclosingFunction" neo4j:"enclosingFunction"`
	FilePath          string `json:"filePath" neo4j:"filePath"`
	StartLine         int    `json:"startLine" neo4j:"startLine"`
	EndLine           int    `json:"endLine" neo4j:"endLine"`
	Isolation         string `json:"isolation,omitempty" neo4j:"isolation,omitempty"`
}

// ProtoContract represents a gRPC service definition from a .proto file.
// It acts as the rendezvous point between callers (GRPCCall nodes) and
// handlers (Function nodes) indexed in separate service runs.
type ProtoContract struct {
	BaseNode
	ProtoPackage string `json:"protoPackage" neo4j:"protoPackage"` // e.g. "fxsvcpb"
	ProtoService string `json:"protoService" neo4j:"protoService"` // e.g. "FXService"
	ProtoFile    string `json:"protoFile" neo4j:"protoFile"`       // relative path to .proto file
	ServiceName  string `json:"serviceName" neo4j:"serviceName"`   // owning service (--service flag)
}

// ProtoMethod represents a single RPC method defined in a proto service.
type ProtoMethod struct {
	BaseNode
	Name         string `json:"name" neo4j:"name"`
	RequestType  string `json:"requestType" neo4j:"requestType"`
	ResponseType string `json:"responseType" neo4j:"responseType"`
}

// NodeFactory creates nodes from maps (useful for Neo4j result parsing)
func NodeFactory(nodeType NodeType, props map[string]any) interface{} {
	now := time.Now()
	
	switch nodeType {
	case ServiceNode:
		return &Service{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case FileNode:
		return &File{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case ModuleNode:
		return &Module{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case ClassNode:
		return &Class{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case InterfaceNode:
		return &Interface{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case FunctionNode:
		return &Function{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case MethodNode:
		return &Method{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case VariableNode:
		return &Variable{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case ParameterNode:
		return &Parameter{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case SymbolNode:
		return &Symbol{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case GRPCCallNode:
		return &GRPCCall{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case HTTPCallNode:
		return &HTTPCall{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case OutboxCallNode:
		return &OutboxCall{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case EventTypeNode:
		return &EventType{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case DBCallNode:
		return &DBCall{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case ExternalCallNode:
		return &ExternalCall{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case ExternalServiceNode:
		return &ExternalService{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case CommentNode:
		return &Comment{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case DocumentNode:
		return &Document{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case DocumentChunkNode:
		return &DocumentChunk{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case FlowNode:
		return &Flow{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case FeatureNode:
		return &Feature{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case PullRequestNode:
		return &PullRequest{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case GeneratedDocNode:
		return &GeneratedDoc{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case GenerationDiagnosticNode:
		return &GenerationDiagnostic{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case ControlFlowScopeNode:
		return &ControlFlowScope{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case ConcurrentScopeNode:
		return &ConcurrentScope{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case TxScopeNode:
		return &TxScope{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case ProtoContractNode:
		return &ProtoContract{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	case ProtoMethodNode:
		return &ProtoMethod{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	default:
		return &BaseNode{Props: props, CreatedAt: now, UpdatedAt: now}
	}
}