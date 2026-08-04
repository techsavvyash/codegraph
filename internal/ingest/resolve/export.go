package resolve

// ParseGoSymbolDescriptor is the exported form of parseGoSymbolDescriptor,
// for callers outside this package that need to parse a scip-go symbol
// string into its (pkgPath, typeName, methodName) descriptor without
// string-constructing symbols themselves — see parseGoSymbolDescriptor's
// doc comment for the grammar. Added for internal/verify/oracle's Go
// differential call-graph oracle (RFC-013 Layer 2), which joins onto the
// same symbol strings scip-go actually emitted.
func ParseGoSymbolDescriptor(sym string) (pkgPath, typ, method string, ok bool) {
	return parseGoSymbolDescriptor(sym)
}
