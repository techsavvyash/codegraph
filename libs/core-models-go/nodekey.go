package models

import "fmt"

// NodeKey derivation functions.
// Each function returns a deterministic, human-readable key for a node type.
// These keys are used as merge identifiers to ensure idempotent indexing.
//
// Service-scoping: file paths produced by SCIP are repo-relative, so two
// different services that happen to share a relative path (e.g. both have
// "internal/db/pgx.go", or a common "grpcclient.go") would otherwise collapse
// into a single node when indexed into the same scope. To keep per-service code
// distinct, every path-derived key is prefixed with the owning service name.
// Keys that are already globally unique (SCIP symbols, fully-qualified names)
// are NOT service-scoped, because the SCIP symbol already embeds the package
// path and would only be shared when the underlying definition is genuinely the
// same.

// ServiceNodeKey returns "svc:{name}".
func ServiceNodeKey(name string) string {
	return "svc:" + name
}

// FileNodeKey returns "file:{service}:{path}".
func FileNodeKey(service, path string) string {
	return "file:" + service + ":" + path
}

// SymbolNodeKey returns the SCIP symbol string directly (already globally unique).
func SymbolNodeKey(scipSymbol string) string {
	return scipSymbol
}

// FunctionNodeKey returns "func:{service}:{filePath}#{signature}".
func FunctionNodeKey(service, filePath, signature string) string {
	return "func:" + service + ":" + filePath + "#" + signature
}

// MethodNodeKey returns "method:{service}:{filePath}#{signature}".
func MethodNodeKey(service, filePath, signature string) string {
	return "method:" + service + ":" + filePath + "#" + signature
}

// ClassNodeKey returns "class:{fqn}" if fqn is non-empty (already globally
// unique), else the service-scoped fallback "class:{service}:{filePath}#{name}".
func ClassNodeKey(service, fqn, filePath, name string) string {
	if fqn != "" {
		return "class:" + fqn
	}
	return "class:" + service + ":" + filePath + "#" + name
}

// InterfaceNodeKey returns "iface:{fqn}" if fqn is non-empty (already globally
// unique), else the service-scoped fallback "iface:{service}:{filePath}#{name}".
func InterfaceNodeKey(service, fqn, filePath, name string) string {
	if fqn != "" {
		return "iface:" + fqn
	}
	return "iface:" + service + ":" + filePath + "#" + name
}

// ModuleNodeKey returns "mod:{fqn}".
func ModuleNodeKey(fqn string) string {
	return "mod:" + fqn
}

// VariableNodeKey returns "var:{service}:{filePath}#{name}:{startLine}".
func VariableNodeKey(service, filePath, name string, startLine int) string {
	return fmt.Sprintf("var:%s:%s#%s:%d", service, filePath, name, startLine)
}

// ParameterNodeKey returns "param:{service}:{filePath}#{signature}:{name}:{index}".
func ParameterNodeKey(service, filePath, signature, name string, index int) string {
	return fmt.Sprintf("param:%s:%s#%s:%s:%d", service, filePath, signature, name, index)
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

// CommentNodeKey returns "comment:{service}:{filePath}:{startLine}".
func CommentNodeKey(service, filePath string, startLine int) string {
	return fmt.Sprintf("comment:%s:%s:%d", service, filePath, startLine)
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

// EventTypeNodeKey returns "eventtype:{group}.{action}" — the shared channel hub key.
// It is deliberately NOT service-scoped so producers and consumers across services share
// one node. When action is empty (group-fallback), the key is "eventtype:{group}.*".
func EventTypeNodeKey(group, action string) string {
	if action == "" {
		return "eventtype:" + group + ".*"
	}
	return "eventtype:" + group + "." + action
}

// OutboxCallNodeKey returns "outboxcall:{service}:{filePath}:{transport}:{event}:{destService}:{line}".
// destService is part of the identity because a single call site can fan the same event out to
// several destination services (e.g. the event service relaying to balance AND monitoring), and
// each destination is a distinct physical publish → a distinct OutboxCall node.
// Service-scoped (path-derived) so identical relative paths in different services stay
// distinct. `event` is the semantic event name (or "dynamic"/queue name for non-Async sends).
func OutboxCallNodeKey(service, filePath, transport, event, destService string, line int) string {
	return fmt.Sprintf("outboxcall:%s:%s:%s:%s:%s:%d", service, filePath, transport, event, destService, line)
}

// ReferenceNodeKey returns "ref:{service}:{filePath}:{startLine}:{startColumn}".
func ReferenceNodeKey(service, filePath string, startLine, startColumn int) string {
	return fmt.Sprintf("ref:%s:%s:%d:%d", service, filePath, startLine, startColumn)
}
