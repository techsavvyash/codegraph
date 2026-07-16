package mine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	graph "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/ingest/docs"
	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/internal/model/provenance"
)

// Confidence bands (RFC-011 §6, normative). Every Layer D value sits strictly
// above the semantic layer's 0.60 ceiling.
const (
	confFilepath       = 0.95
	confCodespanLocal  = 0.90
	confCodespanGlobal = 0.85
	confFence          = 0.70
)

// Report summarizes one mining run. Kills are counted, never silent.
type Report struct {
	ChunksMined     int
	EdgesWritten    int
	ByStrategy      map[string]int
	KilledAmbiguous int
	KilledNoMatch   int
	KilledQualifier int
	FenceCapped     int
}

// link is one validated (chunk, target) edge awaiting write.
type link struct {
	chunk      docs.ChunkRecord
	target     NodeRef
	strategy   string
	confidence float64
	reasons    []string
	evidence   []string
}

// decision is the outcome for one candidate: either a link or a counted kill.
type decision struct {
	link *link
	kill string // "", "ambiguous", "nomatch", "qualifier"
}

// Miner validates extracted candidates against the graph and writes MENTIONS
// edges for the survivors.
type Miner struct {
	client      *graph.Client
	serviceName string
	scope       models.ScopeContext
}

// NewMiner creates a Layer D miner for the given service and scope.
func NewMiner(client *graph.Client, serviceName string, scope models.ScopeContext) *Miner {
	return &Miner{client: client, serviceName: serviceName, scope: scope}
}

// MineChunks extracts, validates, and links the given chunks (typically the
// Changed set of an ingest run). Idempotent: edges MERGE on (chunk, target).
func (m *Miner) MineChunks(ctx context.Context, chunks []docs.ChunkRecord) (*Report, error) {
	report := &Report{ByStrategy: map[string]int{}}
	if len(chunks) == 0 {
		return report, nil
	}

	// Phase A: extract everything first so lookups batch across chunks.
	type chunkCands struct {
		chunk docs.ChunkRecord
		cands []Candidate
	}
	perChunk := make([]chunkCands, 0, len(chunks))
	var all []Candidate
	for _, ch := range chunks {
		cands := ExtractCandidates(ch.Content)
		perChunk = append(perChunk, chunkCands{chunk: ch, cands: cands})
		all = append(all, cands...)
	}
	report.ChunksMined = len(chunks)

	// Phase B: batch-load lookup tables.
	res := &resolver{client: m.client, serviceName: m.serviceName, scopeID: m.scope.ScopeID}
	tbl, err := res.load(ctx, all)
	if err != nil {
		return nil, err
	}

	// Phase C+D: decide per chunk, dedup per (chunk, target), cap fences.
	var links []link
	for _, cc := range perChunk {
		links = append(links, m.decideChunk(cc.chunk, cc.cands, tbl, report)...)
	}

	// Phase E: provenanced edge writes.
	if err := m.writeLinks(ctx, links, report); err != nil {
		return nil, err
	}

	return report, nil
}

// decideChunk runs the pure decision pipeline for one chunk.
func (m *Miner) decideChunk(chunk docs.ChunkRecord, cands []Candidate, tbl *lookupTables, report *Report) []link {
	// Dedup per target: highest confidence wins; all firing matchers are
	// recorded in reasons.
	byTarget := map[string]*link{}

	fenceLinks := 0
	for _, c := range cands {
		d := decide(c, tbl)
		if d.kill != "" {
			switch d.kill {
			case "ambiguous":
				report.KilledAmbiguous++
			case "qualifier":
				report.KilledQualifier++
			default:
				report.KilledNoMatch++
			}
			continue
		}

		l := d.link
		l.chunk = chunk

		if c.Kind == FenceCandidate {
			if existing := byTarget[l.target.ElementID]; existing == nil {
				if fenceLinks >= maxFenceLinksPerChunk {
					report.FenceCapped++
					continue
				}
				fenceLinks++
			}
		}

		if existing, ok := byTarget[l.target.ElementID]; ok {
			existing.reasons = mergeReasons(existing.reasons, l.reasons)
			existing.evidence = append(existing.evidence, l.evidence...)
			if l.confidence > existing.confidence {
				existing.confidence = l.confidence
				existing.strategy = l.strategy
			}
			continue
		}
		byTarget[l.target.ElementID] = l
	}

	out := make([]link, 0, len(byTarget))
	for _, l := range byTarget {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].target.NodeKey < out[j].target.NodeKey })
	return out
}

// decide is the pure validation core: one candidate against the tables.
func decide(c Candidate, tbl *lookupTables) decision {
	switch c.Kind {
	case PathCandidate:
		return decidePath(c, tbl)
	default:
		return decideName(c, tbl)
	}
}

func decidePath(c Candidate, tbl *lookupTables) decision {
	refs := matchServiceFiles(tbl.serviceFiles, c.Name)
	reasons := []string{"explicit-path"}
	if len(refs) == 0 {
		refs = tbl.globalFileMatches[c.Name]
		reasons = append(reasons, "cross-service")
	}
	switch len(refs) {
	case 0:
		return decision{kill: "nomatch"}
	case 1:
		return decision{link: &link{
			target:     refs[0],
			strategy:   "docmine/filepath",
			confidence: confFilepath,
			reasons:    reasons,
			evidence:   []string{evidenceRef(c)},
		}}
	default:
		return decision{kill: "ambiguous"}
	}
}

func decideName(c Candidate, tbl *lookupTables) decision {
	refs := tbl.byName[c.Name]
	if len(refs) == 0 {
		return decision{kill: "nomatch"}
	}

	// A qualifier must corroborate: the resolved node's signature or nodeKey
	// carries the qualifying type/package name, else the candidate dies.
	if c.Qualifier != "" {
		refs = filterByQualifier(refs, c.Qualifier)
		if len(refs) == 0 {
			return decision{kill: "qualifier"}
		}
	}

	var inService []NodeRef
	for _, r := range refs {
		if r.InService {
			inService = append(inService, r)
		}
	}

	strategy := "docmine/codespan"
	if c.Kind == FenceCandidate {
		strategy = "docmine/fence"
	}

	if len(inService) == 1 {
		conf := confCodespanLocal
		if c.Kind == FenceCandidate {
			conf = confFence
		}
		return decision{link: &link{
			target:     inService[0],
			strategy:   strategy,
			confidence: conf,
			reasons:    []string{"explicit-identifier", "unique-in-service"},
			evidence:   []string{evidenceRef(c)},
		}}
	}
	if len(inService) > 1 {
		return decision{kill: "ambiguous"}
	}

	// No in-service match. Fence tokens stop here (RFC-011 §5.1 D3: fence
	// identifiers only link within the doc's own service).
	if c.Kind == FenceCandidate {
		return decision{kill: "nomatch"}
	}

	if len(refs) == 1 {
		return decision{link: &link{
			target:     refs[0],
			strategy:   strategy,
			confidence: confCodespanGlobal,
			reasons:    []string{"explicit-identifier", "cross-service", "unique-global"},
			evidence:   []string{evidenceRef(c)},
		}}
	}
	return decision{kill: "ambiguous"}
}

func filterByQualifier(refs []NodeRef, qualifier string) []NodeRef {
	var out []NodeRef
	for _, r := range refs {
		if containsToken(r.Signature, qualifier) || containsToken(r.NodeKey, qualifier) {
			out = append(out, r)
		}
	}
	return out
}

// containsToken reports whether needle occurs in haystack at identifier
// boundaries: "Chunker" matches "(c *Chunker)" and ".../pkg/Chunker#" but not
// "NewChunker" — a bare substring check would let a qualifier corroborate the
// wrong node.
func containsToken(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); {
		j := strings.Index(haystack[i:], needle)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(needle)
		beforeOK := start == 0 || !isIdentChar(haystack[start-1])
		afterOK := end == len(haystack) || !isIdentChar(haystack[end])
		if beforeOK && afterOK {
			return true
		}
		i = start + 1
	}
	return false
}

func isIdentChar(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func evidenceRef(c Candidate) string {
	return fmt.Sprintf("lit:%s@%d", c.Raw, c.Offset)
}

func mergeReasons(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range append(a, b...) {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// writeLinks MERGEs the MENTIONS edges with full provenance (I4). Edge props
// are built exclusively through provenance.BuildMentionEdgeProps, which
// rejects anything missing confidence/reasons/strategy/evidence.
func (m *Miner) writeLinks(ctx context.Context, links []link, report *Report) error {
	if len(links) == 0 {
		return nil
	}

	createdAt := time.Now().UTC().Format(time.RFC3339)
	items := make([]map[string]any, 0, len(links))
	for _, l := range links {
		props, err := provenance.BuildMentionEdgeProps(
			l.confidence, l.reasons, l.strategy, createdAt, m.scope.ScopeID, l.evidence)
		if err != nil {
			return fmt.Errorf("provenance validation failed for %s -> %s: %w", l.chunk.NodeKey, l.target.NodeKey, err)
		}
		items = append(items, map[string]any{
			"fromId": l.chunk.ElementID,
			"toId":   l.target.ElementID,
			"props":  props,
		})
		report.ByStrategy[l.strategy]++
	}

	if err := m.client.MergeRelsBatch(ctx, string(models.MentionsRel), items, 500); err != nil {
		return err
	}
	report.EdgesWritten = len(items)
	return nil
}
