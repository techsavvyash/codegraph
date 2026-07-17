package static

import (
	"bufio"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// funcRange represents the line span of a function/method body in a Go file.
type funcRange struct {
	Name         string
	DeclLine     int      // Line of the function name declaration (matches SCIP's startLine)
	StartLine    int      // Body opening brace line
	EndLine      int      // Body closing brace line
	ParamTypes   []string // Fully-qualified parameter types (e.g., "net/http.ResponseWriter")
	ReceiverType string   // Receiver type for methods (e.g., "*SCIPIndexer")
}


// SCIPCallGraphBuilder infers CALLS relationships between Functions/Methods
// by correlating Go AST call sites with indexed Function/Method nodes.
type SCIPCallGraphBuilder struct {
	client      *neo4j.Client
	projectPath string
	modulePath  string // Go module path from go.mod, used to filter external targets
	serviceName string // Service node name used to restrict listGoFiles to this module only
	scopeCtx    models.ScopeContext
œ	// moduleNodes is the service-wide bare-name → elementId map for cross-file callee
	// resolution. Only free functions (no receiver) are keyed by bare name. Methods are
	// keyed by "ReceiverType.Name" (e.g. "RedisCache.Get") to prevent cross-receiver
	// collision under last-write-wins.
	moduleNodes map[string]string

	// Precise resolution data, derived from SCIP occurrences (see LoadSCIPResolution).
	// These let processFile bind a call site to the EXACT callee SCIP resolved,
	// instead of guessing by bare name (which collides across receiver types and
	// silently resolved to an arbitrary node under last-write-wins).
	symbolToNodeID map[string]string // symbolNodeKey → elementId (Function/Method definitions)
	occIndex       map[string]string // occKey(file,line,name) → callee symbolNodeKey
	// ifaceToImpl bridges interface-method calls to their concrete implementation.
	// SCIP resolves a call like repo.Payout.UpdateStatusByID(...) to the INTERFACE
	// method symbol (no body, no DB effect); the effect-bearing node is the concrete
	// impl, reachable via IMPLEMENTS. Keyed by interface-method symbolNodeKey →
	// concrete impl elementId. Only unambiguous (single-impl) interfaces are kept.
	ifaceToImpl map[string]string
	// filteredSymbolKeys holds SCIP symbol keys whose definition lives in a generated
	// or filtered file (e.g. *.pb.go, /gen/, /vendor/). resolvePreciseCallee returns
	// the sentinel calleeFiltered for these so the call site skips CALLS-edge creation
	// without falling through to bare-name fallback.
	filteredSymbolKeys map[string]bool
	// elementIDToReceiverType maps each Method node's element ID to its receiverType
	// property (e.g. "*SubmitEntityFields"). Used by processFile to validate that a
	// SCIP-precise resolution actually makes sense for the calling file: if the callee's
	// receiver type doesn't appear anywhere in the source of the calling file, SCIP made
	// a wrong cross-type resolution (e.g. resolving req.GetEmail() on a proto request to
	// a same-named adapter method on an unrelated struct). Populated in loadModuleNodes.
	elementIDToReceiverType map[string]string

	// Resolution telemetry (logged at end of BuildCallGraph) so a single index run
	// reveals whether precise resolution actually fired vs degraded to bare-name.
	preciseHits    int
	fallbackHits   int
	filteredSkips  int
	wrongTypeDrops int // SCIP precise hits dropped due to receiver-type mismatch
}

// calleeFiltered is a sentinel returned by resolvePreciseCallee when SCIP resolved
// the call site to a symbol defined in a generated/filtered file (proto getter, etc.).
// The call site loop must skip CALLS-edge creation without attempting bare-name fallback.
const calleeFiltered = "__filtered__"

// bareNameFallbackDenylist contains method/function names so common in Go that
// bare-name fallback is almost certainly wrong for them. Any call whose SCIP
// occurrence is missing AND whose name is in this set is silently dropped rather
// than bound to an arbitrary same-named node in the service.
var bareNameFallbackDenylist = map[string]bool{
	// Standard interface methods that appear on every driver and framework type.
	"Get":    true,
	"Set":    true,
	"Delete": true,
	"Close":  true,
	"Ping":   true,
	"Do":     true,
	"Exec":   true,
	"Query":  true,
	"Scan":   true,
	"Next":   true,
	"Err":    true,
	"Error":  true,
	"Read":   true,
	"Write":  true,
	"String": true,
	"Reset":  true,
	"Flush":  true,
}

// LoadSCIPResolution populates the precise call-resolution maps from the parsed
// SCIP symbol definitions and the node index produced by indexSymbols. After this
// is called, processFile resolves callees by SCIP symbol identity (exact) and only
// falls back to bare-name matching when no occurrence covers a call site.
//
//   - symbolToNodeID maps each Function/Method symbol to its graph node.
//   - occIndex maps every call-site occurrence (file + line + method name) to the
//     symbol it references, so a call site → exact callee lookup is O(1).
//
// SCIP ranges are 0-based; AST/token positions are 1-based, so occurrence lines
// are normalised by +1 to match the call lines parsed from the AST.
func (cg *SCIPCallGraphBuilder) LoadSCIPResolution(ctx context.Context, symbolDefs []*models.SymbolDefinition, idx *symbolIndex) {
	cg.symbolToNodeID = make(map[string]string)
	if idx != nil {
		for symKey, defKey := range idx.symbolToDefKey {
			if id, ok := idx.defIDs[defKey]; ok {
				cg.symbolToNodeID[symKey] = id
			}
		}
	}

	cg.filteredSymbolKeys = make(map[string]bool)
	cg.occIndex = make(map[string]string)
	for _, sd := range symbolDefs {
		if sd == nil || sd.Info == nil || sd.Info.Symbol == nil {
			continue
		}
		symStr := sd.Info.Symbol.String()
		// Index callable references: functions/methods, plus any type member
		// (symbol contains '#'). The latter captures INTERFACE methods, which SCIP
		// classifies as kind=Type but which call sites resolve to — these are bridged
		// to their concrete impl via ifaceToImpl below. Package-level vars/consts
		// (no '#', non-callable) are excluded to keep the index focused.
		if !isFunctionReferenceKind(sd.Info.Kind) && !strings.Contains(symStr, "#") {
			continue
		}
		symKey := models.SymbolNodeKey(symStr)

		// Mark symbols whose call sites must NOT fall through to bare-name matching.
		// resolvePreciseCallee returns the calleeFiltered sentinel for these, so a
		// call SCIP definitively resolved is skipped rather than mis-bound to an
		// unrelated same-named node. Two distinct cases:
		//
		//  (1) Definition lives in a generated file we index (*.pb.go, /gen/). The
		//      node exists but is noise — catch it via the definition file path.
		//
		//  (2) Definition lives OUTSIDE this module (proto getters from the shared
		//      proto module, stdlib, third-party deps). SCIP still emits usage
		//      occurrences at our call sites and resolves them to the external
		//      symbol, but GetDefinitionReference() is nil because the definition is
		//      not in our index — so case (1) never fires for them. These are the
		//      req.GetEmail()/GetName()/GetType() proto getters: without this branch
		//      resolvePreciseCallee returns "" (indistinguishable from "unresolved")
		//      and bare-name fallback binds them to an arbitrary same-named
		//      method/handler in this service (e.g. *SubmitEntityFields.GetEmail).
		if defRef := sd.GetDefinitionReference(); defRef != nil {
			if isGeneratedFilePath(defRef.FilePath) {
				cg.filteredSymbolKeys[symKey] = true
			}
		} else if cg.isExternalSymbol(symStr) {
			cg.filteredSymbolKeys[symKey] = true
		}

		// DisplayName carries the SCIP descriptor suffix (e.g. "UpdateStatusByID().");
		// strip it so the key matches the bare identifier the AST yields at call sites
		// (call.TargetName == "UpdateStatusByID"). Without this every lookup misses and
		// resolution silently degrades to bare-name matching.
		name := normalizeIdent(sd.Info.DisplayName)
		if name == "" {
			continue
		}
		for _, ref := range sd.Refs {
			if ref == nil || ref.IsDefinition {
				continue // definitions are not call sites
			}
			astLine := ref.StartLine + 1 // SCIP 0-based → AST 1-based
			cg.occIndex[occKey(ref.FilePath, astLine, name)] = symKey
		}
	}

	cg.loadInterfaceImpls(ctx)

	fmt.Printf("  Loaded SCIP resolution: %d symbol→node entries, %d call-site occurrences, %d interface→impl bridges, %d filtered symbols\n",
		len(cg.symbolToNodeID), len(cg.occIndex), len(cg.ifaceToImpl), len(cg.filteredSymbolKeys))
}

// loadInterfaceImpls builds the interface-method → concrete-impl bridge from the
// IMPLEMENTS edges already written to the graph (concrete Method/Function →
// interface Symbol). Only interfaces with a single implementation are kept: with
// multiple impls the concrete callee can't be chosen without runtime type info, so
// we leave those to bare-name fallback rather than bind arbitrarily.
func (cg *SCIPCallGraphBuilder) loadInterfaceImpls(ctx context.Context) {
	cg.ifaceToImpl = make(map[string]string)
	rows, err := cg.client.ExecuteQuery(ctx, `
		MATCH (m)-[:IMPLEMENTS]->(s:Symbol)
		WHERE (m:Method OR m:Function) AND (m.scopeId = $scopeId OR m.scopeId = 'main')
		RETURN s.symbol AS sym, elementId(m) AS implId`,
		map[string]any{"scopeId": cg.scopeCtx.ScopeID})
	if err != nil {
		fmt.Printf("  Warning: interface-impl bridge query failed: %v\n", err)
		return
	}
	ambiguous := make(map[string]bool)
	for _, row := range rows {
		rm := row.AsMap()
		sym := getStringFromMap(rm, "sym")
		implID := getStringFromMap(rm, "implId")
		if sym == "" || implID == "" {
			continue
		}
		key := models.SymbolNodeKey(sym)
		if existing, ok := cg.ifaceToImpl[key]; ok {
			if existing != implID {
				ambiguous[key] = true
			}
			continue
		}
		cg.ifaceToImpl[key] = implID
	}
	for k := range ambiguous {
		delete(cg.ifaceToImpl, k)
	}
}

// isExternalSymbol reports whether a SCIP symbol string names a definition that
// lives outside this service's Go module (the shared proto module, stdlib, or any
// third-party dependency). SCIP symbol strings embed the defining module, e.g.
//
//	scip-go gomod github.com/tazapay/proto v1.6.83 `.../account/grpc/v1`/ForgotPasswordRequest#GetEmail().
//	scip-go gomod github.com/tazapay/account v1.0.0 `.../utils`/SubmitEntityFields#GetEmail().
//
// An in-module symbol always carries this service's module path; an external one
// (proto getters, deps) does not. When modulePath is unknown (empty go.mod) we
// cannot judge, so we conservatively report false and leave such symbols to the
// normal resolution path. This is what lets proto getters be filtered even though
// their definitions never appear in our index (GetDefinitionReference() == nil).
func (cg *SCIPCallGraphBuilder) isExternalSymbol(symStr string) bool {
	if cg.modulePath == "" {
		return false
	}
	return !strings.Contains(symStr, cg.modulePath)
}

// occKey builds the composite key used by occIndex. NUL separators avoid any
// collision between path/name segments.
func occKey(filePath string, line int, name string) string {
	return filePath + "\x00" + strconv.Itoa(line) + "\x00" + name
}

// resolvePreciseCallee returns the elementId of the exact callee SCIP resolved for
// the call site at (filePath, line, name), or "" when no occurrence covers it.
// Returns (calleeFiltered, false) when SCIP resolved the call to a symbol defined in a
// generated/filtered file (e.g. proto getters in *.pb.go) — callers must skip
// CALLS-edge creation for that sentinel without falling back to bare-name matching.
// The second return value (viaInterface) is true when the resolution used the
// ifaceToImpl bridge rather than symbolToNodeID directly. Callers use this to decide
// whether to apply the receiver-type plausibility check (only for direct resolutions).
// A ±1 line tolerance absorbs any residual 0-/1-based drift between SCIP ranges and
// AST token positions without sacrificing per-receiver precision.
func (cg *SCIPCallGraphBuilder) resolvePreciseCallee(filePath string, line int, name string) (string, bool) {
	if len(cg.occIndex) == 0 {
		return "", false
	}
	for _, l := range [3]int{line, line - 1, line + 1} {
		symKey, ok := cg.occIndex[occKey(filePath, l, name)]
		if !ok {
			continue
		}
		// Concrete function/method: bind directly.
		if id, ok := cg.symbolToNodeID[symKey]; ok {
			return id, false
		}
		// Interface method (no concrete node): bridge to its sole implementation.
		if id, ok := cg.ifaceToImpl[symKey]; ok {
			return id, true
		}
		// Symbol defined in a filtered/generated file (e.g. proto getter).
		// Return the sentinel so the caller skips bare-name fallback — there is no
		// point searching for a same-named node in the service for these.
		if cg.filteredSymbolKeys[symKey] {
			return calleeFiltered, false
		}
	}
	return "", false
}

// NewSCIPCallGraphBuilder creates a new call graph builder.
func NewSCIPCallGraphBuilder(client *neo4j.Client, projectPath string) *SCIPCallGraphBuilder {
	return &SCIPCallGraphBuilder{
		client:      client,
		projectPath: projectPath,
		modulePath:  readModulePath(projectPath),
		scopeCtx:    models.DefaultScope(),
	}
}

// SetServiceName restricts the file list to only files owned by the named
// Service node. This prevents the builder from attempting to parse files
// from other modules that were indexed independently.
func (cg *SCIPCallGraphBuilder) SetServiceName(name string) {
	cg.serviceName = name
}

// readModulePath reads the module path from go.mod in the given directory.
// Returns empty string if go.mod is missing or malformed, which disables
// filtering (CONTAINS "" is always true).
func readModulePath(projectPath string) string {
	f, err := os.Open(filepath.Join(projectPath, "go.mod"))
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

// SetScope sets the scope context for the builder.
func (cg *SCIPCallGraphBuilder) SetScope(scope models.ScopeContext) {
	cg.scopeCtx = scope
}

// BuildCallGraph infers CALLS relationships for all Go source files in the graph.
func (cg *SCIPCallGraphBuilder) BuildCallGraph(ctx context.Context) error {
	fmt.Println("Building call graph from SCIP references...")

	// Pre-load all Function/Method IDs for the service so processFile can resolve
	// cross-file callees without querying Reference nodes (which are suppressed).
	if err := cg.loadModuleNodes(ctx); err != nil {
		fmt.Printf("Warning: failed to pre-load module nodes: %v\n", err)
		cg.moduleNodes = make(map[string]string)
	}
	fmt.Printf("  Pre-loaded %d function/method nodes for cross-file resolution\n", len(cg.moduleNodes))

	// Get all Go source files in the graph.
	files, err := cg.listGoFiles(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Go files: %w", err)
	}

	totalCalls := 0
	for _, filePath := range files {
		n, err := cg.processFile(ctx, filePath)
		if err != nil {
			fmt.Printf("Warning: call graph for %s: %v\n", filePath, err)
			continue
		}
		totalCalls += n
	}

	fmt.Printf("Call graph complete: created %d CALLS relationships across %d files\n", totalCalls, len(files))
	fmt.Printf("  Callee resolution: %d precise (SCIP symbol), %d bare-name fallback, %d filtered/skipped (proto/generated), %d dropped (wrong receiver type)\n",
		cg.preciseHits, cg.fallbackHits, cg.filteredSkips, cg.wrongTypeDrops)

	// Compute in/out degree properties on all Function/Method nodes in scope.
	if err := cg.ComputeDegreeProperties(ctx); err != nil {
		fmt.Printf("Warning: degree computation failed: %v\n", err)
	}

	return nil
}

// ComputeDegreeProperties sets inDegree and outDegree properties on all
// Function/Method nodes in the current scope based on CALLS relationships.
func (cg *SCIPCallGraphBuilder) ComputeDegreeProperties(ctx context.Context) error {
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		OPTIONAL MATCH (fn)<-[:CALLS]-(caller)
		WHERE (caller:Function OR caller:Method)
		  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
		OPTIONAL MATCH (fn)-[:CALLS]->(callee)
		WHERE (callee:Function OR callee:Method)
		  AND (callee.scopeId = $scopeId OR callee.scopeId = 'main')
		WITH fn, count(DISTINCT caller) AS inD, count(DISTINCT callee) AS outD
		SET fn.inDegree = inD, fn.outDegree = outD
	`
	_, err := cg.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId": cg.scopeCtx.ScopeID,
	})
	if err != nil {
		return fmt.Errorf("degree computation: %w", err)
	}
	fmt.Println("Computed inDegree/outDegree for all Function/Method nodes")
	return nil
}

// updateFunctionBodyRanges updates the startLine and endLine properties on
// Function/Method nodes to reflect the actual AST body range (Lbrace to Rbrace).
// SCIP indexing only stores the declaration line, which makes line-based
// containment queries (like findContainingFunction) fail.
func (cg *SCIPCallGraphBuilder) updateFunctionBodyRanges(ctx context.Context, callers []callerInfo) error {
	cypher := `
		UNWIND $updates AS u
		MATCH (fn) WHERE elementId(fn) = u.id
		SET fn.startLine = u.startLine,
		    fn.endLine = u.endLine,
		    fn.paramTypes = u.paramTypes,
		    fn.receiverType = u.receiverType
	`
	updates := make([]map[string]any, len(callers))
	for i, c := range callers {
		updates[i] = map[string]any{
			"id":           c.ID,
			"startLine":    c.StartLine,
			"endLine":      c.EndLine,
			"paramTypes":   c.ParamTypes,
			"receiverType": c.ReceiverType,
		}
	}
	_, err := cg.client.ExecuteQuery(ctx, cypher, map[string]any{"updates": updates})
	return err
}

// listGoFiles returns all file paths with a .go extension that are indexed in the graph.
// When serviceName is set, only files owned by that Service node are returned, preventing
// cross-module path mismatches during call graph construction.
func (cg *SCIPCallGraphBuilder) listGoFiles(ctx context.Context) ([]string, error) {
	var query string
	var params map[string]any

	if cg.serviceName != "" {
		query = `
			MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(f:File)
			WHERE f.path ENDS WITH '.go'
			  AND f.scopeId = $scopeId
			RETURN f.path AS path
		`
		params = map[string]any{
			"scopeId":     cg.scopeCtx.ScopeID,
			"serviceName": cg.serviceName,
		}
	} else {
		query = `
			MATCH (f:File)
			WHERE f.path ENDS WITH '.go'
			  AND f.scopeId = $scopeId
			RETURN f.path AS path
		`
		params = map[string]any{
			"scopeId": cg.scopeCtx.ScopeID,
		}
	}

	results, err := cg.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(results))
	for _, rec := range results {
		p := getStringFromMap(rec.AsMap(), "path")
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// callerInfo pairs an AST-derived body range with the graph node element ID.
type callerInfo struct {
	ID           string   // Neo4j element ID
	StartLine    int      // AST body start line
	EndLine      int      // AST body end line
	ParamTypes   []string // Fully-qualified parameter types
	ReceiverType string   // Receiver type for methods
}

// processFile parses a single Go file with the AST to get function body ranges,
// maps them to graph node IDs, queries references, and creates CALLS edges.
func (cg *SCIPCallGraphBuilder) processFile(ctx context.Context, filePath string) (int, error) {
	fullPath := filePath
	if !filepath.IsAbs(filePath) {
		fullPath = filepath.Join(cg.projectPath, filePath)
	}

	// Parse Go source for function body ranges (AST gives us real start/end).
	funcRanges, err := parseFuncRanges(fullPath)
	if err != nil {
		return 0, err
	}
	if len(funcRanges) == 0 {
		return 0, nil
	}

	// Parse control flow scopes for conditional metadata and ControlFlowScope node creation.
	cfScopes, _ := parseControlFlowScopes(fullPath)
	// Prefix nodeKeys and parentScopeKeys with scopeId for graph uniqueness.
	scopeIDPrefix := cg.scopeCtx.ScopeID + ":"
	for i := range cfScopes {
		cfScopes[i].NodeKey = scopeIDPrefix + cfScopes[i].NodeKey
		if cfScopes[i].ParentScopeKey != "" {
			cfScopes[i].ParentScopeKey = scopeIDPrefix + cfScopes[i].ParentScopeKey
		}
	}

	// Parse concurrent (goroutine / errgroup) and transactional scopes so calls
	// inside them can be tagged via IN_PARALLEL_WITH / IN_TX (Change #3).
	concScopes, txScopes, _ := parseConcurrentAndTxScopes(fullPath)
	for i := range concScopes {
		concScopes[i].NodeKey = scopeIDPrefix + concScopes[i].NodeKey
	}
	for i := range txScopes {
		txScopes[i].NodeKey = scopeIDPrefix + txScopes[i].NodeKey
	}

	// Parse AST-derived enrichment (order index, literal args, nearest
	// comment, receiver chain) keyed by "<line>:<methodName>". Best-effort:
	// a parse failure here only loses enrichment, not the edge itself.
	callMetadata, _ := parseCallMetadata(fullPath)

	// Load graph node IDs for functions in this file, keyed by base name.
	// We filter to only functions whose SCIP signature matches this file's
	// Go package path, so cross-package references stored as Function nodes
	// are excluded.
	graphNodes, err := cg.graphNodesByName(ctx, filePath)
	if err != nil {
		return 0, err
	}

	// Build callerInfo list: pair each AST func range with its graph ID.
	var callers []callerInfo
	for _, fr := range funcRanges {
		// Use base name (strip receiver prefix like "SCIPIndexer.")
		baseName := fr.Name
		if idx := strings.LastIndex(baseName, "."); idx >= 0 {
			baseName = baseName[idx+1:]
		}
		nodeID, ok := graphNodes[baseName]
		if !ok {
			continue
		}
		callers = append(callers, callerInfo{
			ID:           nodeID,
			StartLine:    fr.StartLine,
			EndLine:      fr.EndLine,
			ParamTypes:   fr.ParamTypes,
			ReceiverType: fr.ReceiverType,
		})
	}

	if len(callers) == 0 {
		return 0, nil
	}

	// Update graph nodes with AST-derived body ranges so that line-based
	// lookups (e.g., findContainingFunction in API analysis) work correctly.
	// SCIP only stores the declaration line; the AST gives us the real body range.
	if err := cg.updateFunctionBodyRanges(ctx, callers); err != nil {
		fmt.Printf("Warning: failed to update body ranges for %s: %v\n", filePath, err)
	}

	// Build per-function call sites from the Go AST.
	// Reference nodes are suppressed in noiseNodeLabels so there are no
	// Reference → Symbol → DEFINES → Function chains to query. We derive
	// CALLS edges directly from the parsed AST instead.
	astCalls, astErr := parseASTCallsPerFunc(fullPath)
	if astErr != nil {
		fmt.Printf("Warning: AST call parse failed for %s: %v\n", filePath, astErr)
		astCalls = make(map[int][]astCallSite)
	}

	// Read file source for receiver-type plausibility checks below.
	// OS page cache means this is effectively free after parseFuncRanges already read it.
	fileSource, _ := os.ReadFile(fullPath)

	created := 0
	seen := map[string]bool{}

	for _, fr := range funcRanges {
		callerBaseName := fr.Name
		if idx := strings.LastIndex(callerBaseName, "."); idx >= 0 {
			callerBaseName = callerBaseName[idx+1:]
		}
		callerID, ok := graphNodes[callerBaseName]
		if !ok {
			continue
		}

		for _, call := range astCalls[fr.DeclLine] {
			// Prefer PRECISE resolution: bind to the exact callee SCIP resolved for
			// this call site, disambiguating receiver types that share a method name
			// (e.g. the many distinct Get()/UpdateStatusByID() across repositories).
			calleeID, viaInterface := cg.resolvePreciseCallee(filePath, call.Line, call.TargetName)
			if calleeID == calleeFiltered {
				// SCIP identified the callee as a generated/filtered symbol (proto getter
				// etc.). Skip edge creation entirely — bare-name fallback would only
				// misattribute this to a same-named service function.
				cg.filteredSkips++
				continue
			}
			// Receiver-type plausibility check: SCIP sometimes resolves a method call
			// (e.g. req.GetEmail() on a proto request) to a completely unrelated method
			// with the same name on a different receiver type (e.g. *SubmitEntityFields).
			// When the resolved callee has a receiverType that doesn't appear anywhere in
			// the calling file's source text, the resolution is clearly wrong. We apply
			// this check only for direct resolutions (not interface bridges), because
			// the concrete impl of an interface may legitimately be in a package the
			// caller never imports directly.
			if calleeID != "" && !viaInterface {
				if rt := cg.elementIDToReceiverType[calleeID]; rt != "" {
					typeName := strings.TrimPrefix(rt, "*")
					if len(fileSource) > 0 && !strings.Contains(string(fileSource), typeName) {
						cg.wrongTypeDrops++
						calleeID = ""
					}
				}
			}
			if calleeID != "" {
				cg.preciseHits++
			}
			// Fall back to bare-name matching only when no occurrence covers the site
			// (preserves edges SCIP didn't resolve). Same-file first, then the
			// service-wide map for cross-file calls.
			//
			// Guard: names in bareNameFallbackDenylist are too common across Go
			// interfaces, drivers, and framework types to safely resolve by bare name.
			// Dropping the edge is always better than creating a wrong one.
			if calleeID == "" && !bareNameFallbackDenylist[call.TargetName] {
				calleeID = graphNodes[call.TargetName]
				if calleeID == "" {
					calleeID = cg.moduleNodes[call.TargetName]
				}
				if calleeID != "" {
					cg.fallbackHits++
				}
			}
			if calleeID == "" {
				continue // external or unindexed callee
			}

			// Skip DB/repository call sites — these are already represented as
			// CALLS_DB edges created by the DBCallDetector. Creating a CALLS edge
			// on top of them is redundant noise. We detect them via the receiver
			// chain stored in callMetadata (computed from the same AST).
			if cm, hasCM := callMetadata[fmt.Sprintf("%d:%s", call.Line, call.TargetName)]; hasCM {
				if isDBCallSite(call.TargetName, cm.ReceiverChain) {
					continue
				}
			}

			pairKey := callerID + "->" + calleeID
			if callerID == calleeID || seen[pairKey] {
				continue
			}
			seen[pairKey] = true

			innerScope := findInnermostScope(cfScopes, call.Line)
			innerConc := findInnermostConcurrentScope(concScopes, call.Line)
			innerTx := findInnermostTxScope(txScopes, call.Line)
			var depth int
			var isCond bool
			if innerScope != nil {
				depth = innerScope.Depth
				isCond = true
			}

			setProps := map[string]any{
				"line":          call.Line,
				"filePath":      filePath,
				"branchDepth":   depth,
				"isConditional": isCond,
				"isParallel":    innerConc != nil,
				"isInTx":        innerTx != nil,
			}

			var meta callMeta
			if m, ok := callMetadata[fmt.Sprintf("%d:%s", call.Line, call.TargetName)]; ok {
				meta = m
				if meta.OrderIndex > 0 {
					setProps["orderIndex"] = meta.OrderIndex
				}
				if len(meta.LiteralArgs) > 0 {
					setProps["literalArgs"] = meta.LiteralArgs
				}
				if meta.NearestComment != "" {
					setProps["nearestComment"] = meta.NearestComment
				}
				if len(meta.ReceiverChain) > 0 {
					setProps["receiverChain"] = meta.ReceiverChain
				}
			}

			_, err := cg.client.MergeRelationship(ctx, callerID, calleeID, string(models.CallsRel),
				nil, setProps)
			if err != nil {
				fmt.Printf("Warning: failed to create CALLS edge: %v\n", err)
				continue
			}
			created++

		}
	}

	return created, nil
}

// graphNodesByName returns a map of base function name -> element ID for
// functions in the given file whose SCIP signature matches the file's
// Go package directory. This filters out cross-package reference nodes
// that SCIP stores as Function nodes in the same filePath.
func (cg *SCIPCallGraphBuilder) graphNodesByName(ctx context.Context, filePath string) (map[string]string, error) {
	// Derive the Go package directory from the file path (e.g.,
	// "pkg/indexer/static/scip_indexer.go" -> "pkg/indexer/static").
	pkgDir := filepath.Dir(filePath)

	query := `
		MATCH (f)
		WHERE (f:Function OR f:Method)
		  AND f.filePath = $filePath
		  AND f.scopeId = $scopeId
		  AND f.signature CONTAINS $pkgDir
		RETURN elementId(f) AS id, f.name AS name
	`

	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath": filePath,
		"scopeId":  cg.scopeCtx.ScopeID,
		"pkgDir":   pkgDir,
	})
	if err != nil {
		return nil, err
	}

	m := make(map[string]string, len(results))
	for _, rec := range results {
		rm := rec.AsMap()
		id := getStringFromMap(rm, "id")
		name := getStringFromMap(rm, "name")
		if id != "" && name != "" {
			baseName := strings.TrimSuffix(name, "().")
			m[baseName] = id
		}
	}
	return m, nil
}

// parseFuncRanges parses a Go source file and returns the line ranges for each
// function and method body.
func parseFuncRanges(filePath string) ([]funcRange, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return nil, err
	}

	// Collect import paths so we can resolve qualified type names.
	importMap := buildImportMap(f)

	var ranges []funcRange
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		name := fn.Name.Name
		var receiverType string
		// For methods, include the receiver type in the name to disambiguate.
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			recvType := exprName(fn.Recv.List[0].Type)
			if recvType != "" {
				name = recvType + "." + name
				receiverType = exprTypeName(fn.Recv.List[0].Type)
			}
		}

		// Extract parameter types.
		var paramTypes []string
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				typeName := resolveTypeName(field.Type, importMap)
				// A field may declare multiple names (e.g., a, b int).
				count := len(field.Names)
				if count == 0 {
					count = 1 // unnamed parameter
				}
				for range count {
					paramTypes = append(paramTypes, typeName)
				}
			}
		}

		declLine := fset.Position(fn.Name.Pos()).Line
		startLine := fset.Position(fn.Body.Lbrace).Line
		endLine := fset.Position(fn.Body.Rbrace).Line

		ranges = append(ranges, funcRange{
			Name:         name,
			DeclLine:     declLine,
			StartLine:    startLine,
			EndLine:      endLine,
			ParamTypes:   paramTypes,
			ReceiverType: receiverType,
		})
	}

	return ranges, nil
}

// buildImportMap builds a mapping from local package alias/name to import path
// for a Go file's imports. For example, "http" -> "net/http", "models" -> "github.com/...".
func buildImportMap(f *ast.File) map[string]string {
	m := make(map[string]string)
	for _, imp := range f.Imports {
		if imp.Path == nil {
			continue
		}
		importPath := strings.Trim(imp.Path.Value, `"`)
		var localName string
		if imp.Name != nil {
			localName = imp.Name.Name
		} else {
			// Default: last segment of import path
			parts := strings.Split(importPath, "/")
			localName = parts[len(parts)-1]
		}
		if localName != "_" && localName != "." {
			m[localName] = importPath
		}
	}
	return m
}

// resolveTypeName resolves an AST type expression to a qualified type string,
// using the import map to resolve package-qualified names (e.g., http.Request -> net/http.Request).
func resolveTypeName(expr ast.Expr, importMap map[string]string) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + resolveTypeName(t.X, importMap)
	case *ast.SelectorExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			if fullPkg, found := importMap[ident.Name]; found {
				return fullPkg + "." + t.Sel.Name
			}
			return ident.Name + "." + t.Sel.Name
		}
		return t.Sel.Name
	case *ast.ArrayType:
		return "[]" + resolveTypeName(t.Elt, importMap)
	case *ast.MapType:
		return "map[" + resolveTypeName(t.Key, importMap) + "]" + resolveTypeName(t.Value, importMap)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func"
	case *ast.ChanType:
		return "chan " + resolveTypeName(t.Value, importMap)
	case *ast.Ellipsis:
		return "..." + resolveTypeName(t.Elt, importMap)
	case *ast.IndexExpr:
		return resolveTypeName(t.X, importMap) + "[" + resolveTypeName(t.Index, importMap) + "]"
	case *ast.IndexListExpr:
		return resolveTypeName(t.X, importMap)
	case *ast.StructType:
		return "struct{}"
	}
	return "unknown"
}

// exprTypeName extracts a string representation of a receiver type expression,
// including pointer markers (e.g., "*SCIPIndexer").
func exprTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprTypeName(t.X)
	case *ast.IndexExpr:
		return exprTypeName(t.X)
	case *ast.IndexListExpr:
		return exprTypeName(t.X)
	}
	return ""
}



// exprName extracts the type name from a receiver expression, handling
// pointer receivers like *Foo.
func exprName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return exprName(t.X)
	case *ast.IndexExpr:
		return exprName(t.X)
	case *ast.IndexListExpr:
		return exprName(t.X)
	}
	return ""
}

// findEnclosingFunc finds the innermost function whose body range contains
// the given line number.
func findEnclosingFunc(ranges []funcRange, line int) *funcRange {
	var best *funcRange
	bestSpan := int(^uint(0) >> 1) // max int

	for i := range ranges {
		r := &ranges[i]
		if line >= r.StartLine && line <= r.EndLine {
			span := r.EndLine - r.StartLine
			if span < bestSpan {
				best = r
				bestSpan = span
			}
		}
	}
	return best
}

// isGoFile checks whether a path ends with .go
func isGoFile(path string) bool {
	return strings.HasSuffix(path, ".go")
}

// isDBCallSite reports whether a call site is a repository/DB operation that
// is already captured by the DBCallDetector as a CALLS_DB edge. Such sites must
// not also produce a CALLS edge — doing so creates a redundant parallel path.
//
// Two patterns are blocked:
//  1. The repository accessor call itself (target == "Pgx") — e.g. repository.Pgx()
//  2. Any call whose receiver chain passes through the accessor — e.g. the chain
//     ["repository", "Pgx()", "PayoutDocument", "GetByID"] for a .GetByID() call.
//
// "Pgx()" (with the "()" suffix) is the marker written by extractReceiverChain
// when a segment was itself an invocation, making this a precise signal.
func isDBCallSite(targetName string, receiverChain []string) bool {
	if targetName == "Pgx" {
		return true
	}
	for _, part := range receiverChain {
		if part == "Pgx()" {
			return true
		}
	}
	return false
}

// loadModuleNodes pre-loads all Function/Method node IDs for the service into
// moduleNodes. Called once before the per-file loop to enable cross-file callee
// resolution without Reference nodes.
//
// Key scheme (prevents cross-receiver last-write-wins collision):
//   - Free functions (no receiverType): keyed by bare name, e.g. "ParseToken".
//   - Methods:  keyed by "ReceiverType.Name", e.g. "RedisCache.Get".
//
// Same-file graphNodes lookups in processFile take priority over this map.
// The bare-name denylist in processFile prevents ambiguous names (Get, Set, etc.)
// from being resolved even when an entry exists here.
func (cg *SCIPCallGraphBuilder) loadModuleNodes(ctx context.Context) error {
	query := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.scopeId = $scopeId
		RETURN fn.name AS name, coalesce(fn.receiverType, '') AS receiverType, elementId(fn) AS id
	`
	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"scopeId": cg.scopeCtx.ScopeID,
	})
	if err != nil {
		return err
	}
	cg.moduleNodes = make(map[string]string, len(results))
	cg.elementIDToReceiverType = make(map[string]string, len(results))
	for _, rec := range results {
		rm := rec.AsMap()
		name := getStringFromMap(rm, "name")
		id := getStringFromMap(rm, "id")
		receiverType := getStringFromMap(rm, "receiverType")
		if name == "" || id == "" {
			continue
		}
		// Strip SCIP descriptor suffixes (e.g. "GetByID().").
		name = strings.TrimSuffix(name, "().")
		name = strings.TrimSuffix(name, "()")

		if receiverType != "" {
			// Method: key by "ReceiverType.Name" — strips pointer marker so both
			// *RedisCache and RedisCache resolve to the same key "RedisCache.Get".
			receiver := strings.TrimPrefix(receiverType, "*")
			cg.moduleNodes[receiver+"."+name] = id
			// Track receiver type for cross-type SCIP resolution validation.
			cg.elementIDToReceiverType[id] = receiverType
		} else {
			// Free function: bare name is unambiguous within the service.
			cg.moduleNodes[name] = id
		}
	}
	return nil
}

// astCallSite holds an AST-derived call site's line and bare callee name.
type astCallSite struct {
	Line       int
	TargetName string
}

// parseASTCallsPerFunc parses filePath and returns per-function call sites.
// Result is keyed by the function's declaration line (funcRange.DeclLine).
// Uses callTargetName/callTargetPos from call_metadata.go to extract the
// same bare identifier that the SCIP metadata enrichment uses as its key.
func parseASTCallsPerFunc(filePath string) (map[int][]astCallSite, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return nil, err
	}

	out := make(map[int][]astCallSite)
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		declLine := fset.Position(fn.Name.Pos()).Line
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callTargetName(ce)
			if name == "" {
				return true
			}
			line := fset.Position(callTargetPos(ce)).Line
			out[declLine] = append(out[declLine], astCallSite{
				Line:       line,
				TargetName: name,
			})
			return true
		})
	}
	return out, nil
}
