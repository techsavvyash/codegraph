package models

import "fmt"

// NodeKey derivation functions.
// Each function returns a deterministic, human-readable key for a node type.
// These keys are used as merge identifiers to ensure idempotent indexing.
//
// Path-based node keys (File, Function, Method, Variable, Parameter, Comment,
// Reference, and the fallback paths of Class/Interface) require a serviceName
// argument because file paths emitted by SCIP indexers are module-relative —
// e.g., scip-go writes "main.go" for both apps/cli/main.go and
// apps/mcp-server-go/main.go. Without service disambiguation, two modules'
// files merge into a single Neo4j node and their child symbols collapse.
//
// serviceName is expected to be the unique service identifier registered on
// the corresponding Service node (e.g., "codegraph/apps/cli"). It must not
// contain ':' since that's the key separator; '/' is fine.

// ServiceNodeKey returns "svc:{name}".
func ServiceNodeKey(name string) string {
	return "svc:" + name
}

// FileNodeKey returns "file:{serviceName}:{path}". The serviceName prefix
// disambiguates module-relative paths that would otherwise collide across
// services in a monorepo (see package doc).
func FileNodeKey(serviceName, path string) string {
	return "file:" + serviceName + ":" + path
}

// SymbolNodeKey returns the SCIP symbol string directly (already globally unique).
func SymbolNodeKey(scipSymbol string) string {
	return scipSymbol
}

// FunctionNodeKey returns "func:{serviceName}:{filePath}#{signature}".
func FunctionNodeKey(serviceName, filePath, signature string) string {
	return "func:" + serviceName + ":" + filePath + "#" + signature
}

// MethodNodeKey returns "method:{serviceName}:{filePath}#{signature}".
func MethodNodeKey(serviceName, filePath, signature string) string {
	return "method:" + serviceName + ":" + filePath + "#" + signature
}

// ClassNodeKey returns "class:{fqn}" if fqn is non-empty (SCIP symbols are
// globally unique and don't need a service prefix), else
// "class:{serviceName}:{filePath}#{name}".
func ClassNodeKey(fqn, serviceName, filePath, name string) string {
	if fqn != "" {
		return "class:" + fqn
	}
	return "class:" + serviceName + ":" + filePath + "#" + name
}

// InterfaceNodeKey returns "iface:{fqn}" if fqn is non-empty, else
// "iface:{serviceName}:{filePath}#{name}".
func InterfaceNodeKey(fqn, serviceName, filePath, name string) string {
	if fqn != "" {
		return "iface:" + fqn
	}
	return "iface:" + serviceName + ":" + filePath + "#" + name
}

// ModuleNodeKey returns "mod:{fqn}".
func ModuleNodeKey(fqn string) string {
	return "mod:" + fqn
}

// VariableNodeKey returns "var:{serviceName}:{filePath}#{name}:{startLine}".
func VariableNodeKey(serviceName, filePath, name string, startLine int) string {
	return fmt.Sprintf("var:%s:%s#%s:%d", serviceName, filePath, name, startLine)
}

// ParameterNodeKey returns "param:{serviceName}:{filePath}#{signature}:{name}:{index}".
func ParameterNodeKey(serviceName, filePath, signature, name string, index int) string {
	return fmt.Sprintf("param:%s:%s#%s:%s:%d", serviceName, filePath, signature, name, index)
}

// DocumentNodeKey returns "doc:{sourceUrl}".
func DocumentNodeKey(sourceURL string) string {
	return "doc:" + sourceURL
}

// DocumentChunkNodeKey returns "chunk:{documentKey}#{chunkIndex}".
func DocumentChunkNodeKey(documentKey string, chunkIndex int) string {
	return fmt.Sprintf("chunk:%s#%d", documentKey, chunkIndex)
}

// FeatureNodeKey returns "feat:{name}".
func FeatureNodeKey(name string) string {
	return "feat:" + name
}

// APIRouteNodeKey returns "api:{method}:{path}".
func APIRouteNodeKey(method, path string) string {
	return "api:" + method + ":" + path
}

// CommentNodeKey returns "comment:{serviceName}:{filePath}:{startLine}".
func CommentNodeKey(serviceName, filePath string, startLine int) string {
	return fmt.Sprintf("comment:%s:%s:%d", serviceName, filePath, startLine)
}

// FlowNodeKey returns "flow:{flowType}:{entrypointKey}".
func FlowNodeKey(flowType, entrypointKey string) string {
	return "flow:" + flowType + ":" + entrypointKey
}

// PullRequestNodeKey returns "pr:{prID}".
func PullRequestNodeKey(prID string) string {
	return "pr:" + prID
}

// GeneratedDocNodeKey returns "gendoc:{type}:{sourceKey}".
func GeneratedDocNodeKey(docType, sourceKey string) string {
	return "gendoc:" + docType + ":" + sourceKey
}

// GenerationDiagnosticNodeKey returns "gendiag:{type}:{sourceKey}".
func GenerationDiagnosticNodeKey(docType, sourceKey string) string {
	return "gendiag:" + docType + ":" + sourceKey
}

// SDKCallNodeKey returns "sdkcall:{target}".
func SDKCallNodeKey(target string) string {
	return "sdkcall:" + target
}

// ReferenceNodeKey returns "ref:{serviceName}:{filePath}:{startLine}:{startColumn}".
func ReferenceNodeKey(serviceName, filePath string, startLine, startColumn int) string {
	return fmt.Sprintf("ref:%s:%s:%d:%d", serviceName, filePath, startLine, startColumn)
}
