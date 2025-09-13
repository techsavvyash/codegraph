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
	APIRouteNode  NodeType = "APIRoute"
	CommentNode   NodeType = "Comment"
	DocumentNode  NodeType = "Document"
	FeatureNode   NodeType = "Feature"
)

// BaseNode represents common properties for all nodes
type BaseNode struct {
	ID        string            `json:"id,omitempty" neo4j:"id,omitempty"`
	Labels    []string          `json:"labels,omitempty" neo4j:"labels,omitempty"`
	Props     map[string]any    `json:"properties,omitempty" neo4j:"properties,omitempty"`
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
	Name        string `json:"name" neo4j:"name"`
	Signature   string `json:"signature" neo4j:"signature"`
	ReturnType  string `json:"returnType" neo4j:"returnType"`
	FilePath    string `json:"filePath" neo4j:"filePath"`
	StartLine   int    `json:"startLine" neo4j:"startLine"`
	EndLine     int    `json:"endLine" neo4j:"endLine"`
	IsExported  bool   `json:"isExported" neo4j:"isExported"`
	IsAsync     bool   `json:"isAsync" neo4j:"isAsync"`
	Complexity  int    `json:"complexity" neo4j:"complexity"`
	Docstring   string `json:"docstring" neo4j:"docstring"`
}

// Method represents an instance method belonging to a class
type Method struct {
	BaseNode
	Name           string `json:"name" neo4j:"name"`
	Signature      string `json:"signature" neo4j:"signature"`
	ReturnType     string `json:"returnType" neo4j:"returnType"`
	AccessModifier string `json:"accessModifier" neo4j:"accessModifier"`
	FilePath       string `json:"filePath" neo4j:"filePath"`
	StartLine      int    `json:"startLine" neo4j:"startLine"`
	EndLine        int    `json:"endLine" neo4j:"endLine"`
	IsStatic       bool   `json:"isStatic" neo4j:"isStatic"`
	IsAbstract     bool   `json:"isAbstract" neo4j:"isAbstract"`
	IsOverride     bool   `json:"isOverride" neo4j:"isOverride"`
	Complexity     int    `json:"complexity" neo4j:"complexity"`
	Docstring      string `json:"docstring" neo4j:"docstring"`
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

// APIRoute represents an exposed API endpoint
type APIRoute struct {
	BaseNode
	Path         string `json:"path" neo4j:"path"`
	Method       string `json:"method" neo4j:"method"`
	Protocol     string `json:"protocol" neo4j:"protocol"`
	Description  string `json:"description" neo4j:"description"`
	IsDeprecated bool   `json:"isDeprecated" neo4j:"isDeprecated"`
	Version      string `json:"version" neo4j:"version"`
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

// Feature represents a specific feature or capability
type Feature struct {
	BaseNode
	Name        string   `json:"name" neo4j:"name"`
	Description string   `json:"description" neo4j:"description"`
	Status      string   `json:"status" neo4j:"status"`
	Priority    string   `json:"priority" neo4j:"priority"`
	Tags        []string `json:"tags" neo4j:"tags"`
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
	case APIRouteNode:
		return &APIRoute{
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
	case FeatureNode:
		return &Feature{
			BaseNode: BaseNode{Props: props, CreatedAt: now, UpdatedAt: now},
		}
	default:
		return &BaseNode{Props: props, CreatedAt: now, UpdatedAt: now}
	}
}