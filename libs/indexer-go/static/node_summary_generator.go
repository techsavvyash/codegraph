package static

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"unicode"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	neo4j "github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// GenerateAndStoreNodeSummaries queries all Function/Method nodes in scope that
// lack a summary, generates one deterministically for each, and writes them back
// in a single UNWIND round-trip.  It is safe to call multiple times (idempotent).
// Returns the number of nodes updated.
//
// The summary is built from two signals, in order of value:
//  1. the node's *effects* — the DB tables it writes/reads, the services it calls,
//     and the events it emits, read directly from the CALLS_DB / CALLS_API edges.
//     This is the high-signal half: it tells the reader something the identifier
//     alone never could.
//  2. a *lead* phrase from the docstring (preferred) or the name. This frames the
//     intent; on its own it mostly restates the name, so it is only the scaffold
//     the effects hang off.
func GenerateAndStoreNodeSummaries(ctx context.Context, client *neo4j.Client, scopeCtx models.ScopeContext) (int, error) {
	const fetchLimit = 5000

	// Each summary describes the node ONE HOP out: its own direct effects (the DB
	// tables it queries, the services it calls, the events it emits) plus the names
	// of the functions it directly calls. We deliberately do NOT roll effects up the
	// whole subtree — the card is a local navigation map, and an LLM walks the graph
	// hop by hop using the listed callees. One-hop also keeps cards small (the point
	// is to save tokens) and sidesteps the order/cycle hazards of transitive roll-up.
	directEffects, err := fetchDirectEffects(ctx, client, scopeCtx.ScopeID)
	if err != nil {
		return 0, fmt.Errorf("GenerateAndStoreNodeSummaries: %w", err)
	}
	directCallees, err := fetchDirectCallees(ctx, client, scopeCtx.ScopeID)
	if err != nil {
		return 0, fmt.Errorf("GenerateAndStoreNodeSummaries: %w", err)
	}

	// Regenerate for ALL Function/Method nodes in scope on every run — NOT only
	// those lacking a summary. Summaries are derived from the call graph, which is
	// rebuilt each index; a "skip if already set" guard leaves stale summaries from a
	// previous (possibly buggy) run in place, requiring a manual clear before every
	// re-index. Recomputing all (a few thousand) nodes is cheap and keeps summaries
	// consistent with the current graph by construction.
	metaCypher := `
MATCH (n) WHERE (n:Function OR n:Method)
  AND n.scopeId = $scopeId
RETURN n.nodeKey AS nodeKey,
       n.name AS name,
       n.goSignature AS goSignature,
       n.docstring AS docstring,
       coalesce(n.isRPCHandler, false) AS isRPCHandler
LIMIT $limit`

	rows, err := client.ExecuteQuery(ctx, metaCypher, map[string]any{
		"scopeId": scopeCtx.ScopeID,
		"limit":   fetchLimit,
	})
	if err != nil {
		return 0, fmt.Errorf("GenerateAndStoreNodeSummaries: fetch failed: %w", err)
	}

	type update struct {
		NodeKey string
		Summary string
	}
	updates := make([]update, 0, len(rows))
	for _, row := range rows {
		m := row.AsMap()
		nodeKey, _ := m["nodeKey"].(string)
		if nodeKey == "" {
			continue
		}
		isRPCHandler, _ := m["isRPCHandler"].(bool)
		eff := NodeEffects{}
		if re, ok := directEffects[nodeKey]; ok {
			eff = re.resolve()
		}
		eff.Functions = directCallees[nodeKey]
		in := NodeSummaryInput{
			Name:         stringFromMap(m, "name"),
			GoSignature:  stringFromMap(m, "goSignature"),
			Docstring:    stringFromMap(m, "docstring"),
			IsRPCHandler: isRPCHandler,
			Effects:      eff,
		}
		if s := GenerateNodeSummary(in); s != "" {
			updates = append(updates, update{NodeKey: nodeKey, Summary: s})
		}
	}

	if len(updates) == 0 {
		log.Printf("[NodeSummaries] all nodes already have summaries, skipping")
		return 0, nil
	}

	batch := make([]map[string]any, len(updates))
	for i, u := range updates {
		batch[i] = map[string]any{"nodeKey": u.NodeKey, "summary": u.Summary}
	}
	unwindCypher := `
UNWIND $batch AS item
MATCH (n {nodeKey: item.nodeKey})
SET n.summary = item.summary`

	if _, err := client.ExecuteQuery(ctx, unwindCypher, map[string]any{"batch": batch}); err != nil {
		return 0, fmt.Errorf("GenerateAndStoreNodeSummaries: batch write failed: %w", err)
	}

	log.Printf("[NodeSummaries] wrote summaries for %d/%d nodes", len(updates), len(rows))
	return len(updates), nil
}

func stringFromMap(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// rawEffects accumulates a node's side-effects as ordered sets before the
// write/read overlap is resolved. Writes and reads are kept apart so the overlap
// can be subtracted once, after the whole subtree has been merged in.
type rawEffects struct {
	writes *orderedSet
	reads  *orderedSet
	calls  *orderedSet
	events *orderedSet
}

func newRawEffects() *rawEffects {
	return &rawEffects{
		writes: newOrderedSet(),
		reads:  newOrderedSet(),
		calls:  newOrderedSet(),
		events: newOrderedSet(),
	}
}

// resolve produces the public NodeEffects, dropping any table from reads that is
// also written somewhere in the subtree.
func (r *rawEffects) resolve() NodeEffects {
	reads := make([]string, 0, len(r.reads.items))
	for _, t := range r.reads.items {
		if !r.writes.has(t) {
			reads = append(reads, t)
		}
	}
	return NodeEffects{
		DBWrites: r.writes.items,
		DBReads:  reads,
		Calls:    r.calls.items,
		Events:   r.events.items,
	}
}

// fetchDirectEffects loads every Function/Method's 1-hop effects, keyed by nodeKey.
// Each edge type is queried separately to avoid a cartesian product between the
// DB / RPC / event matches on hot nodes.
func fetchDirectEffects(ctx context.Context, client *neo4j.Client, scopeID string) (map[string]*rawEffects, error) {
	out := map[string]*rawEffects{}
	get := func(key string) *rawEffects {
		if e, ok := out[key]; ok {
			return e
		}
		e := newRawEffects()
		out[key] = e
		return e
	}
	params := map[string]any{"scopeId": scopeID}

	dbRows, err := client.ExecuteQuery(ctx, `
MATCH (n)-[:CALLS_DB]->(db:DBCall)
WHERE (n:Function OR n:Method) AND n.scopeId = $scopeId
RETURN n.nodeKey AS nodeKey, db.table AS tbl, db.operation AS op`, params)
	if err != nil {
		return nil, fmt.Errorf("fetch DB effects: %w", err)
	}
	for _, row := range dbRows {
		m := row.AsMap()
		key := str(m["nodeKey"])
		tbl := strings.TrimSpace(str(m["tbl"]))
		if key == "" || tbl == "" {
			continue
		}
		if isWriteOp(str(m["op"])) {
			get(key).writes.add(tbl)
		} else {
			get(key).reads.add(tbl)
		}
	}

	grpcRows, err := client.ExecuteQuery(ctx, `
MATCH (n)-[:CALLS_API]->(g:GRPCCall)-[:CALLS_SERVICE]->(s:Service)
WHERE (n:Function OR n:Method) AND n.scopeId = $scopeId
RETURN n.nodeKey AS nodeKey, s.name AS svc, g.targetMethod AS method`, params)
	if err != nil {
		return nil, fmt.Errorf("fetch gRPC effects: %w", err)
	}
	for _, row := range grpcRows {
		m := row.AsMap()
		key := str(m["nodeKey"])
		if key == "" {
			continue
		}
		svc := strings.TrimSpace(str(m["svc"]))
		method := normalizeIdent(str(m["method"]))
		switch {
		case svc != "" && method != "":
			get(key).calls.add(svc + "." + method)
		case svc != "":
			get(key).calls.add(svc)
		case method != "":
			get(key).calls.add(method)
		}
	}

	httpRows, err := client.ExecuteQuery(ctx, `
MATCH (n)-[:CALLS_API]->(h:HTTPCall)-[:CALLS_SERVICE]->(s:Service)
WHERE (n:Function OR n:Method) AND n.scopeId = $scopeId
RETURN n.nodeKey AS nodeKey, s.name AS svc`, params)
	if err != nil {
		return nil, fmt.Errorf("fetch HTTP effects: %w", err)
	}
	for _, row := range httpRows {
		m := row.AsMap()
		key := str(m["nodeKey"])
		if svc := strings.TrimSpace(str(m["svc"])); key != "" && svc != "" {
			get(key).calls.add(svc)
		}
	}

	evRows, err := client.ExecuteQuery(ctx, `
MATCH (n)-[:CALLS_API]->(ev:OutboxCall)
WHERE (n:Function OR n:Method) AND n.scopeId = $scopeId
RETURN n.nodeKey AS nodeKey, ev.eventType AS event, ev.queueOrTopic AS topic`, params)
	if err != nil {
		return nil, fmt.Errorf("fetch event effects: %w", err)
	}
	for _, row := range evRows {
		m := row.AsMap()
		key := str(m["nodeKey"])
		if key == "" {
			continue
		}
		ev := strings.TrimSpace(str(m["event"]))
		if ev == "" {
			ev = strings.TrimSpace(str(m["topic"]))
		}
		if ev != "" {
			get(key).events.add(ev)
		}
	}

	return out, nil
}

// fetchDirectCallees loads, per caller, the display names of the Function/Method
// nodes it directly CALLS (one hop, intra-service). These names are the heart of
// the navigation card: they tell an LLM exactly which functions to expand next.
// Names are normalised (SCIP descriptor noise stripped) and de-duplicated.
//
// Callees are returned in EXECUTION order, i.e. ascending call-site line within
// the caller's body (the CALLS edge carries `line`, set to the first call site
// for that pair). Source order is the standard heuristic for execution order and
// lets an LLM read the card as a sequence — "first get, then act, then respond" —
// rather than an unordered set. The ORDER BY drives the row order that the
// orderedSet below preserves; edges without a line (e.g. from the legacy AST
// builder) sort last via coalesce.
func fetchDirectCallees(ctx context.Context, client *neo4j.Client, scopeID string) (map[string][]string, error) {
	rows, err := client.ExecuteQuery(ctx, `
MATCH (a)-[r:CALLS]->(b)
WHERE (a:Function OR a:Method) AND (b:Function OR b:Method)
  AND a.scopeId = $scopeId AND b.scopeId = $scopeId
RETURN a.nodeKey AS src, b.name AS name
ORDER BY a.nodeKey, coalesce(r.line, 2147483647)`, map[string]any{"scopeId": scopeID})
	if err != nil {
		return nil, fmt.Errorf("fetch CALLS edges: %w", err)
	}
	sets := map[string]*orderedSet{}
	for _, row := range rows {
		m := row.AsMap()
		src := str(m["src"])
		name := normalizeIdent(str(m["name"]))
		if src == "" || name == "" {
			continue
		}
		s, ok := sets[src]
		if !ok {
			s = newOrderedSet()
			sets[src] = s
		}
		s.add(name)
	}
	out := make(map[string][]string, len(sets))
	for k, s := range sets {
		out[k] = s.items
	}
	return out, nil
}

// NodeEffects captures the structured side-effects of a function/method, derived
// from its outgoing CALLS_DB / CALLS_API edges. These are the high-signal facts
// the bare identifier cannot convey.
type NodeEffects struct {
	Functions []string // names of directly-called intra-service functions/methods
	DBWrites  []string // tables with a mutating op (INSERT/UPDATE/DELETE/UPSERT/MERGE)
	DBReads   []string // tables only read (SELECT), excluding any also written
	Calls     []string // downstream targets: "service.method" (gRPC) or "service" (HTTP)
	Events    []string // emitted event types / topics
}

// NodeSummaryInput holds the fields needed to produce a one-line summary.
type NodeSummaryInput struct {
	Name         string
	GoSignature  string // rendered Go signature, e.g. "func F(ctx, req *Req) (*Resp, error)"
	Docstring    string
	IsRPCHandler bool
	ReceiverType string // non-empty for methods
	Effects      NodeEffects
}

// GenerateNodeSummary returns a short description of a function or method derived
// entirely from structured data — no LLM required.
//
// Shape:  "<lead> — <effects>"   e.g.
//
//	"Upserts payout error mappings — writes `lookup_payout_error`, calls `sol.UpsertPayoutPSPError`"
//
// The lead frames intent (docstring first sentence, else a verb phrase from the
// name); the effects carry the information the name cannot. When effects are
// absent the lead stands alone.
func GenerateNodeSummary(in NodeSummaryInput) string {
	in.Name = normalizeIdent(in.Name)

	// A concrete object to fall back on when the name yields none (e.g. a bare
	// "Upsert" method): prefer the primary written table, then the first call.
	primaryObj := primaryEffectObject(in.Effects)

	lead := summaryFromDocstring(in.Name, in.Docstring)
	// Only docstring *prose* is used to suppress redundant effect tokens (a
	// comment that already says "into lookup_payout_error" shouldn't be echoed as
	// "writes lookup_payout_error"). A name-derived lead must NOT suppress effects:
	// a function "FundPayout" writing the "payout" table would otherwise lose its
	// "writes payout" — the word coincides, but the fact is not redundant.
	dedupAgainst := lead
	if lead == "" {
		lead = summaryFromName(in, primaryObj)
	}
	if lead == "" {
		return ""
	}

	// The argument clause names the request/response types (not every field) so
	// the reader knows the call's shape without opening it. It rides right after
	// the purpose: "Funds a payout (FundPayoutRequest→FundPayoutResponse)".
	if args := argsClause(in.GoSignature); args != "" {
		lead = lead + " " + args
	}

	effectClause := formatEffects(in.Effects, dedupAgainst)
	if effectClause == "" {
		return clampLen(lead, 280)
	}
	return clampLen(lead+" — "+effectClause, 280)
}

// argsClause renders the request/response types as "(Req→Resp)" from a Go
// signature. It deliberately surfaces only the type *names* — not the proto
// field list — because the card is for triage, not reading; a per-field dump
// would bloat the line and work against the token-saving goal. Returns "" when
// no meaningful (non-builtin) request type can be parsed.
func argsClause(goSig string) string {
	params, returns := splitSignature(goSig)
	req := firstMeaningfulType(params)
	resp := firstMeaningfulType(returns)
	switch {
	case req != "" && resp != "":
		return "(" + req + "→" + resp + ")"
	case req != "":
		return "(" + req + ")"
	default:
		return ""
	}
}

// splitSignature breaks a Go signature into its parameter list and return list
// (both as raw, comma-separated strings). It tolerates an optional receiver and
// a parenthesised or bare return clause. Returns empty strings when the input is
// not a parseable signature.
func splitSignature(goSig string) (params, returns string) {
	s := strings.TrimSpace(goSig)
	s = strings.TrimPrefix(s, "func ")
	s = strings.TrimSpace(s)
	// Optional method receiver: a leading "(...)" before the method name.
	if strings.HasPrefix(s, "(") {
		_, rest, ok := balancedParen(s)
		if !ok {
			return "", ""
		}
		s = strings.TrimSpace(rest)
	}
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return "", ""
	}
	inner, rest, ok := balancedParen(s[open:])
	if !ok {
		return "", ""
	}
	params = inner
	returns = strings.TrimSpace(rest)
	// A multi-value return is parenthesised: "(a, b error)". Unwrap it.
	if strings.HasPrefix(returns, "(") {
		if r2, _, ok := balancedParen(returns); ok {
			returns = r2
		}
	}
	return params, returns
}

// balancedParen expects s to start with '(' and returns the content between it
// and its matching ')', plus everything after that ')'.
func balancedParen(s string) (inner, rest string, ok bool) {
	if len(s) == 0 || s[0] != '(' {
		return "", "", false
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[1:i], s[i+1:], true
			}
		}
	}
	return "", "", false
}

// uninterestingType lists types that carry no triage signal as a request/response
// name (plumbing and builtins), so argsClause skips past them to the real domain
// type.
var uninterestingType = map[string]bool{
	"Context": true, "error": true, "bool": true, "string": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"byte": true, "rune": true, "float32": true, "float64": true, "any": true,
}

// firstMeaningfulType returns the base name of the first non-plumbing type in a
// comma-separated param/return list, e.g. "ctx context.Context, req *pb.FooReq"
// → "FooReq".
func firstMeaningfulType(list string) string {
	for _, part := range strings.Split(list, ",") {
		t := typeNameOf(part)
		if t == "" || uninterestingType[t] {
			continue
		}
		return t
	}
	return ""
}

// typeNameOf extracts the base type name from one param/return token, stripping
// the parameter name, pointer/slice/variadic markers, and package qualifier:
// "req *settlement.FundPayoutRequest" → "FundPayoutRequest", "[]*Item" → "Item".
// Returns "" for map/chan/func/anonymous-struct types, which make poor labels.
func typeNameOf(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	fields := strings.Fields(token)
	t := fields[len(fields)-1] // type is the last field (handles named & unnamed)
	t = strings.TrimPrefix(t, "...")
	t = strings.TrimPrefix(t, "[]")
	t = strings.TrimLeft(t, "*&")
	if strings.ContainsAny(t, "[]{}") ||
		strings.HasPrefix(t, "map") || strings.HasPrefix(t, "chan") || strings.HasPrefix(t, "func") {
		return ""
	}
	if dot := strings.LastIndexByte(t, '.'); dot >= 0 {
		t = t[dot+1:]
	}
	return strings.TrimFunc(t, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}

// primaryEffectObject returns a single object word for the name phrase to use
// when the identifier itself supplies none.
func primaryEffectObject(e NodeEffects) string {
	if len(e.DBWrites) > 0 {
		return e.DBWrites[0]
	}
	if len(e.DBReads) > 0 {
		return e.DBReads[0]
	}
	if len(e.Calls) > 0 {
		return e.Calls[0]
	}
	return ""
}

// formatEffects renders the side-effects as a compact clause, dropping any token
// already named in lead (so a docstring that says "into lookup_payout_error" isn't
// echoed) and capping each category to keep the line short.
func formatEffects(e NodeEffects, dedupAgainst string) string {
	leadLower := strings.ToLower(dedupAgainst)
	var parts []string
	add := func(verb string, items []string, max int) {
		filtered := make([]string, 0, len(items))
		for _, it := range items {
			if it == "" || strings.Contains(leadLower, strings.ToLower(it)) {
				continue
			}
			filtered = append(filtered, it)
		}
		if len(filtered) == 0 {
			return
		}
		extra := 0
		if len(filtered) > max {
			extra = len(filtered) - max
			filtered = filtered[:max]
		}
		quoted := make([]string, len(filtered))
		for i, it := range filtered {
			quoted[i] = "`" + it + "`"
		}
		clause := verb + " " + strings.Join(quoted, ", ")
		if extra > 0 {
			clause += fmt.Sprintf(" +%d more", extra)
		}
		parts = append(parts, clause)
	}
	// "calls" merges the directly-called functions with cross-service targets —
	// from a navigator's view both are "where this goes next". Functions come
	// first (the in-service map), then service.method / service targets.
	calls := make([]string, 0, len(e.Functions)+len(e.Calls))
	calls = append(calls, e.Functions...)
	calls = append(calls, e.Calls...)
	add("calls", calls, 5)
	add("writes", e.DBWrites, 3)
	add("reads", e.DBReads, 3)
	add("emits", e.Events, 2)
	return strings.Join(parts, ", ")
}

// summaryFromDocstring extracts the first sentence of a Go doc comment and strips
// the conventional "FuncName " prefix.  Returns "" when the docstring is absent,
// too long to be useful, or identical to the function name alone.
func summaryFromDocstring(name, doc string) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}

	// Grab the first sentence (up to the first period/newline).
	first := doc
	if idx := strings.IndexAny(doc, ".\n"); idx >= 0 {
		first = strings.TrimSpace(doc[:idx])
	}
	if first == "" || first == name {
		return ""
	}

	// Strip the conventional Go doc prefix "FuncName " or "funcName ".
	stripped := first
	if strings.HasPrefix(strings.ToLower(stripped), strings.ToLower(name)+" ") {
		stripped = strings.TrimSpace(stripped[len(name):])
	}
	if stripped == "" {
		return first
	}

	return capitalise(stripped)
}

// summaryFromName produces a verb-phrase summary from the function name alone.
// fallbackObj supplies an object word when the name itself has none (e.g. a bare
// "Upsert" method on a repository).
func summaryFromName(in NodeSummaryInput, fallbackObj string) string {
	name := in.Name

	// RPC handlers get a dedicated prefix — this framing ("entry point") is itself
	// information the bare name lacks, so it's always worth emitting.
	if in.IsRPCHandler {
		return "Handles RPC request: " + camelToWords(name)
	}

	// Try verb-prefix matching (rules are pre-sorted longest-prefix-first).
	// Matching is case-insensitive so unexported helpers ("processFunding") match
	// the same rules as exported ones ("ProcessFunding") — otherwise they fell
	// through to a naive pluraliser that produced "Processs". The match must land
	// on a camel-word boundary so "Settle" is not mistaken for the "Set" verb.
	nameLower := strings.ToLower(name)
	for _, r := range verbRules {
		if !strings.HasPrefix(nameLower, strings.ToLower(r.prefix)) {
			continue
		}
		rest := name[len(r.prefix):]
		if rest != "" {
			if rr := []rune(rest)[0]; unicode.IsLower(rr) {
				continue // not a word boundary, e.g. "Settle" vs "Set"
			}
		}
		if rest == "" {
			return withObject(r.verb, in.ReceiverType, fallbackObj)
		}
		return r.verb + " " + lowerFirst(camelToWords(rest))
	}

	// No prefix matched — split the whole name into words and use them.
	words := splitCamel(name)
	if len(words) == 0 {
		return name
	}
	verb := capitalise(words[0]) + "s"
	if len(words) == 1 {
		return withObject(verb, in.ReceiverType, fallbackObj)
	}
	obj := joinLowered(words[1:])
	return verb + " " + obj
}

// joinLowered lower-cases the first rune of each word (preserving acronyms) and
// joins them with spaces, e.g. ["Balance","Impact","Of","Payout"] →
// "balance impact of payout". Using strings.Join on the raw words would leave the
// internal capitals intact ("balance Impact Of Payout").
func joinLowered(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = lowerFirst(w)
	}
	return strings.Join(out, " ")
}

// withObject appends the best available object to a verb: the receiver type for a
// method, else an effect-derived fallback, else nothing (verb stands alone rather
// than emitting a dangling "Upserts ").
func withObject(verb, receiverType, fallbackObj string) string {
	if receiverType != "" {
		return verb + " " + lowerFirst(camelToWords(receiverType))
	}
	if fallbackObj != "" {
		return verb + " " + fallbackObj
	}
	return verb
}

type verbRule struct {
	prefix string
	verb   string
}

// verbRules is sorted longest-prefix-first in init() so that e.g. "Setup" is not
// shadowed by "Set", or "GetAll" by "Get".
var verbRules = []verbRule{
	{"GetOrCreate", "Gets or creates"},
	{"GetAll", "Retrieves all"},
	{"GetBy", "Retrieves by"},
	{"Get", "Retrieves"},
	{"FetchAll", "Fetches all"},
	{"FetchBy", "Fetches by"},
	{"Fetch", "Fetches"},
	{"FindAll", "Finds all"},
	{"FindBy", "Finds by"},
	{"Find", "Finds"},
	{"ListAll", "Lists all"},
	{"ListBy", "Lists by"},
	{"List", "Lists"},
	{"Search", "Searches for"},
	{"Query", "Queries"},
	{"Lookup", "Looks up"},
	{"Create", "Creates"},
	{"New", "Constructs"},
	{"Make", "Creates"},
	{"Build", "Builds"},
	{"UpdateAll", "Updates all"},
	{"UpdateBy", "Updates by"},
	{"Update", "Updates"},
	{"Set", "Sets"},
	{"Save", "Saves"},
	{"Upsert", "Upserts"},
	{"DeleteAll", "Deletes all"},
	{"DeleteBy", "Deletes by"},
	{"Delete", "Deletes"},
	{"Remove", "Removes"},
	{"Purge", "Purges"},
	{"Archive", "Archives"},
	{"SoftDelete", "Soft-deletes"},
	{"Process", "Processes"},
	{"Handle", "Handles"},
	{"Execute", "Executes"},
	{"Run", "Runs"},
	{"Invoke", "Invokes"},
	{"Validate", "Validates"},
	{"Check", "Checks"},
	{"Verify", "Verifies"},
	{"Assert", "Asserts"},
	{"Ensure", "Ensures"},
	{"Send", "Sends"},
	{"Publish", "Publishes"},
	{"Emit", "Emits"},
	{"Dispatch", "Dispatches"},
	{"Notify", "Notifies"},
	{"Broadcast", "Broadcasts"},
	{"Enqueue", "Enqueues"},
	{"Parse", "Parses"},
	{"Decode", "Decodes"},
	{"Encode", "Encodes"},
	{"Marshal", "Marshals"},
	{"Unmarshal", "Unmarshals"},
	{"Serialize", "Serialises"},
	{"Deserialize", "Deserialises"},
	{"Convert", "Converts"},
	{"Transform", "Transforms"},
	{"Map", "Maps"},
	{"Filter", "Filters"},
	{"Enrich", "Enriches"},
	{"Aggregate", "Aggregates"},
	{"Load", "Loads"},
	{"Store", "Stores"},
	{"Cache", "Caches"},
	{"Invalidate", "Invalidates"},
	{"Evict", "Evicts"},
	{"Register", "Registers"},
	{"Unregister", "Unregisters"},
	{"Subscribe", "Subscribes to"},
	{"Unsubscribe", "Unsubscribes from"},
	{"Start", "Starts"},
	{"Stop", "Stops"},
	{"Shutdown", "Shuts down"},
	{"Init", "Initialises"},
	{"Initialize", "Initialises"},
	{"Initialise", "Initialises"},
	{"Setup", "Sets up"},
	{"Configure", "Configures"},
	{"Close", "Closes"},
	{"Reset", "Resets"},
	{"Refresh", "Refreshes"},
	{"Sync", "Synchronises"},
	{"Lock", "Locks"},
	{"Unlock", "Unlocks"},
	{"Acquire", "Acquires"},
	{"Release", "Releases"},
	{"Open", "Opens"},
	{"Read", "Reads"},
	{"Write", "Writes"},
	{"Append", "Appends"},
	{"Insert", "Inserts"},
	{"Count", "Counts"},
	{"Sum", "Sums"},
	{"Calculate", "Calculates"},
	{"Compute", "Computes"},
	{"Generate", "Generates"},
	{"Merge", "Merges"},
	{"Apply", "Applies"},
	{"Resolve", "Resolves"},
	{"Retry", "Retries"},
	{"Rollback", "Rolls back"},
	{"Commit", "Commits"},
	{"Begin", "Begins"},
	{"End", "Ends"},
	{"Add", "Adds"},
	{"Assign", "Assigns"},
	{"Attach", "Attaches"},
	{"Detach", "Detaches"},
	{"Inject", "Injects"},
	{"Wrap", "Wraps"},
	{"Unwrap", "Unwraps"},
	{"Log", "Logs"},
	{"Track", "Tracks"},
	{"Collect", "Collects"},
	{"Record", "Records"},
	{"Report", "Reports"},
	{"Export", "Exports"},
	{"Import", "Imports"},
	{"Upload", "Uploads"},
	{"Download", "Downloads"},
	{"Is", "Reports whether"},
	{"Has", "Reports whether"},
	{"Can", "Reports whether"},
	{"Should", "Reports whether"},
	{"Must", "Asserts"},
	{"With", "Returns a copy with"},
}

func init() {
	sort.SliceStable(verbRules, func(i, j int) bool {
		return len(verbRules[i].prefix) > len(verbRules[j].prefix)
	})
}

// normalizeIdent strips SCIP descriptor noise that leaks into node names, e.g.
// "Upsert()." or "GetByID()." → "Upsert" / "GetByID", and anything from a '#'
// (SCIP type/member separator) onward.
func normalizeIdent(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(s, "().")
	s = strings.TrimSuffix(s, "()")
	s = strings.TrimSuffix(s, ".")
	return strings.TrimSpace(s)
}

// camelToWords splits a CamelCase identifier into lower-cased words joined by spaces.
// e.g. "GetPayoutByID" → "payout by ID"
func camelToWords(s string) string {
	words := splitCamel(s)
	if len(words) == 0 {
		return s
	}
	result := make([]string, len(words))
	for i, w := range words {
		result[i] = lowerFirst(w)
	}
	return strings.Join(result, " ")
}

// splitCamel splits a CamelCase string into its component words, preserving
// consecutive uppercase sequences (acronyms) as a single token.
// e.g. "GetPayoutByID" → ["Get", "Payout", "By", "ID"]
func splitCamel(s string) []string {
	if s == "" {
		return nil
	}
	var words []string
	start := 0
	runes := []rune(s)
	n := len(runes)
	for i := 1; i < n; i++ {
		curr := runes[i]
		prev := runes[i-1]
		// Start a new word when:
		// (a) current is uppercase and previous is lowercase, e.g. "getUser" → "get" "User"
		// (b) current is uppercase and next is lowercase, e.g. "HTTPClient" → "HTTP" "Client"
		if unicode.IsUpper(curr) {
			if unicode.IsLower(prev) {
				words = append(words, string(runes[start:i]))
				start = i
			} else if i+1 < n && unicode.IsLower(runes[i+1]) {
				words = append(words, string(runes[start:i]))
				start = i
			}
		}
	}
	words = append(words, string(runes[start:]))
	// Remove empty tokens.
	out := words[:0]
	for _, w := range words {
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	// Don't lowercase acronyms (e.g. "ID", "URL").
	if len(r) > 1 && unicode.IsUpper(r[1]) {
		return s
	}
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func clampLen(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

var dbWriteOps = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "UPSERT": true, "MERGE": true,
}

func isWriteOp(op string) bool {
	return dbWriteOps[strings.ToUpper(strings.TrimSpace(op))]
}

// --- small generic helpers for parsing driver result maps ---

func str(v any) string {
	s, _ := v.(string)
	return s
}

// orderedSet preserves insertion order while de-duplicating.
type orderedSet struct {
	seen  map[string]bool
	items []string
}

func newOrderedSet() *orderedSet { return &orderedSet{seen: map[string]bool{}} }

func (o *orderedSet) add(s string) {
	if s == "" || o.seen[s] {
		return
	}
	o.seen[s] = true
	o.items = append(o.items, s)
}

func (o *orderedSet) has(s string) bool { return o.seen[s] }
