package models

import "fmt"

// NodeKey derivation functions.
// Each function returns a deterministic, human-readable key for a node type.
// These keys are used as merge identifiers to ensure idempotent indexing.

// ServiceNodeKey returns "svc:{name}".
func ServiceNodeKey(name string) string {
	return "svc:" + name
}

// FileNodeKey returns "file:{path}".
func FileNodeKey(path string) string {
	return "file:" + path
}

// SymbolNodeKey returns the SCIP symbol string directly (already globally unique).
func SymbolNodeKey(scipSymbol string) string {
	return scipSymbol
}

// FunctionNodeKey returns "func:{filePath}#{signature}".
func FunctionNodeKey(filePath, signature string) string {
	return "func:" + filePath + "#" + signature
}

// MethodNodeKey returns "method:{filePath}#{signature}".
func MethodNodeKey(filePath, signature string) string {
	return "method:" + filePath + "#" + signature
}

// ClassNodeKey returns "class:{fqn}" if fqn is non-empty, else "class:{filePath}#{name}".
func ClassNodeKey(fqn, filePath, name string) string {
	if fqn != "" {
		return "class:" + fqn
	}
	return "class:" + filePath + "#" + name
}

// InterfaceNodeKey returns "iface:{fqn}" if fqn is non-empty, else "iface:{filePath}#{name}".
func InterfaceNodeKey(fqn, filePath, name string) string {
	if fqn != "" {
		return "iface:" + fqn
	}
	return "iface:" + filePath + "#" + name
}

// ModuleNodeKey returns "mod:{fqn}".
func ModuleNodeKey(fqn string) string {
	return "mod:" + fqn
}

// VariableNodeKey returns "var:{filePath}#{name}:{startLine}".
func VariableNodeKey(filePath, name string, startLine int) string {
	return fmt.Sprintf("var:%s#%s:%d", filePath, name, startLine)
}

// ParameterNodeKey returns "param:{filePath}#{signature}:{name}:{index}".
func ParameterNodeKey(filePath, signature, name string, index int) string {
	return fmt.Sprintf("param:%s#%s:%s:%d", filePath, signature, name, index)
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

// CommentNodeKey returns "comment:{filePath}:{startLine}".
func CommentNodeKey(filePath string, startLine int) string {
	return fmt.Sprintf("comment:%s:%d", filePath, startLine)
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

// SDKCallNodeKey returns "sdkcall:{target}".
func SDKCallNodeKey(target string) string {
	return "sdkcall:" + target
}

// ReferenceNodeKey returns "ref:{filePath}:{startLine}:{startColumn}".
func ReferenceNodeKey(filePath string, startLine, startColumn int) string {
	return fmt.Sprintf("ref:%s:%d:%d", filePath, startLine, startColumn)
}
