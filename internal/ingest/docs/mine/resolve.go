package mine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	graph "github.com/context-maximiser/code-graph/internal/graph"
	models "github.com/context-maximiser/code-graph/internal/model"
)

// NodeRef is a resolved graph node a candidate may link to.
type NodeRef struct {
	Label     string
	NodeKey   string
	ElementID string
	Signature string
	InService bool
}

// lookupTables holds everything the pure decision logic needs, batch-loaded
// from the graph once per mining run.
type lookupTables struct {
	// serviceFiles maps File.path → ref for every file of the doc's service.
	serviceFiles map[string]NodeRef
	// byName maps identifier → all resolved definition nodes with that name
	// (Function/Method service-filtered via serviceName; Class/Interface
	// service-affined via packageName containment in the FQN nodeKey).
	byName map[string][]NodeRef
	// globalFileMatches maps a path candidate → files found by the global
	// fallback (only populated for candidates unresolved in-service).
	globalFileMatches map[string][]NodeRef
}

// resolver batch-loads lookupTables for one service+scope.
type resolver struct {
	client      *graph.Client
	serviceName string
	scopeID     string
}

func (r *resolver) load(ctx context.Context, cands []Candidate) (*lookupTables, error) {
	tbl := &lookupTables{
		serviceFiles:      map[string]NodeRef{},
		byName:            map[string][]NodeRef{},
		globalFileMatches: map[string][]NodeRef{},
	}

	if err := r.loadServiceFiles(ctx, tbl); err != nil {
		return nil, err
	}

	packageName, err := r.loadServicePackageName(ctx)
	if err != nil {
		return nil, err
	}

	// Collect the identifier and path candidate sets.
	nameSet := map[string]bool{}
	var pathCands []string
	pathSeen := map[string]bool{}
	for _, c := range cands {
		switch c.Kind {
		case CodespanCandidate, FenceCandidate:
			nameSet[c.Name] = true
		case PathCandidate:
			if !pathSeen[c.Name] {
				pathSeen[c.Name] = true
				pathCands = append(pathCands, c.Name)
			}
		}
	}
	names := make([]string, 0, len(nameSet))
	for n := range nameSet {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic query batches

	if err := r.loadNamedDefinitions(ctx, tbl, names, packageName); err != nil {
		return nil, err
	}

	// Global file fallback only for paths that found nothing in-service.
	for _, p := range pathCands {
		if len(matchServiceFiles(tbl.serviceFiles, p)) > 0 {
			continue
		}
		refs, err := r.globalFileLookup(ctx, p)
		if err != nil {
			return nil, err
		}
		if len(refs) > 0 {
			tbl.globalFileMatches[p] = refs
		}
	}

	return tbl, nil
}

func (r *resolver) loadServiceFiles(ctx context.Context, tbl *lookupTables) error {
	records, err := r.client.ExecuteQuery(ctx, `
		MATCH (f:File {serviceName: $svc, scopeId: $scope})
		RETURN f.path AS path, f.nodeKey AS nodeKey, elementId(f) AS id
	`, map[string]any{"svc": r.serviceName, "scope": r.scopeID})
	if err != nil {
		return fmt.Errorf("failed to load service files: %w", err)
	}
	for _, rec := range records {
		m := rec.AsMap()
		path, _ := m["path"].(string)
		nk, _ := m["nodeKey"].(string)
		id, _ := m["id"].(string)
		if path != "" && id != "" {
			tbl.serviceFiles[path] = NodeRef{Label: "File", NodeKey: nk, ElementID: id, InService: true}
		}
	}
	return nil
}

func (r *resolver) loadServicePackageName(ctx context.Context) (string, error) {
	records, err := r.client.ExecuteQuery(ctx, `
		MATCH (s:Service {nodeKey: $key, scopeId: $scope})
		RETURN coalesce(s.packageName, '') AS packageName
	`, map[string]any{"key": models.ServiceNodeKey(r.serviceName), "scope": r.scopeID})
	if err != nil {
		return "", fmt.Errorf("failed to load service package name: %w", err)
	}
	if len(records) == 0 {
		return "", nil
	}
	pkg, _ := records[0].AsMap()["packageName"].(string)
	return pkg, nil
}

// loadNamedDefinitions resolves candidate identifiers against the four
// definition labels. Function/Method carry serviceName, so in-service
// membership is direct. Class/Interface merge across services on FQN-based
// nodeKeys and carry no serviceName (see computeDefinitionProps); their
// service affinity is the service's packageName appearing in the FQN.
func (r *resolver) loadNamedDefinitions(ctx context.Context, tbl *lookupTables, names []string, packageName string) error {
	if len(names) == 0 {
		return nil
	}

	for _, label := range []string{"Function", "Method"} {
		records, err := r.client.ExecuteQuery(ctx, fmt.Sprintf(`
			UNWIND $names AS nm
			MATCH (n:%s {scopeId: $scope})
			WHERE n.name = nm
			RETURN nm, n.nodeKey AS nodeKey, elementId(n) AS id,
			       coalesce(n.signature, '') AS signature,
			       coalesce(n.serviceName, '') AS serviceName
		`, graph.Ident(label)), map[string]any{"names": names, "scope": r.scopeID})
		if err != nil {
			return fmt.Errorf("failed to resolve %s names: %w", label, err)
		}
		for _, rec := range records {
			m := rec.AsMap()
			nm, _ := m["nm"].(string)
			svc, _ := m["serviceName"].(string)
			ref := NodeRef{
				Label:     label,
				NodeKey:   stringField(m, "nodeKey"),
				ElementID: stringField(m, "id"),
				Signature: stringField(m, "signature"),
				InService: svc == r.serviceName,
			}
			tbl.byName[nm] = append(tbl.byName[nm], ref)
		}
	}

	for _, label := range []string{"Class", "Interface"} {
		records, err := r.client.ExecuteQuery(ctx, fmt.Sprintf(`
			UNWIND $names AS nm
			MATCH (n:%s {scopeId: $scope})
			WHERE n.name = nm
			RETURN nm, n.nodeKey AS nodeKey, elementId(n) AS id
		`, graph.Ident(label)), map[string]any{"names": names, "scope": r.scopeID})
		if err != nil {
			return fmt.Errorf("failed to resolve %s names: %w", label, err)
		}
		for _, rec := range records {
			m := rec.AsMap()
			nm, _ := m["nm"].(string)
			nodeKey := stringField(m, "nodeKey")
			ref := NodeRef{
				Label:     label,
				NodeKey:   nodeKey,
				ElementID: stringField(m, "id"),
				InService: packageName != "" && strings.Contains(nodeKey, packageName),
			}
			tbl.byName[nm] = append(tbl.byName[nm], ref)
		}
	}

	return nil
}

// globalFileLookup finds files anywhere in the scope whose path shares the
// candidate's trailing segments. The ENDS WITH prefilter uses the last two
// segments; exact segment-suffix semantics are enforced by the caller via
// segmentSuffixOverlap.
func (r *resolver) globalFileLookup(ctx context.Context, candidate string) ([]NodeRef, error) {
	segs := strings.Split(strings.Trim(candidate, "/"), "/")
	if len(segs) < 2 {
		return nil, nil
	}
	tail := strings.Join(segs[len(segs)-2:], "/")

	records, err := r.client.ExecuteQuery(ctx, `
		MATCH (f:File {scopeId: $scope})
		WHERE f.path = $tail OR f.path ENDS WITH $suffix
		RETURN f.path AS path, f.nodeKey AS nodeKey, elementId(f) AS id
		LIMIT 25
	`, map[string]any{"scope": r.scopeID, "tail": tail, "suffix": "/" + tail})
	if err != nil {
		return nil, fmt.Errorf("failed global file lookup for %s: %w", candidate, err)
	}

	var refs []NodeRef
	for _, rec := range records {
		m := rec.AsMap()
		path := stringField(m, "path")
		if segmentSuffixOverlap(candidate, path) < 2 {
			continue
		}
		refs = append(refs, NodeRef{
			Label:     "File",
			NodeKey:   stringField(m, "nodeKey"),
			ElementID: stringField(m, "id"),
		})
	}
	return refs, nil
}

// matchServiceFiles returns the service files whose paths segment-suffix
// match the candidate with an overlap of ≥2 segments.
func matchServiceFiles(serviceFiles map[string]NodeRef, candidate string) []NodeRef {
	var refs []NodeRef
	for path, ref := range serviceFiles {
		if segmentSuffixOverlap(candidate, path) >= 2 {
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].NodeKey < refs[j].NodeKey })
	return refs
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}
