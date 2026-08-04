// tsresolver.go implements the SCIP-symbol-join half of RFC-001 Layer 3 for
// TypeScript/JavaScript: parsing tools/ts-resolver/resolve.mjs's JSON
// output, parsing scip-typescript's symbol-string grammar, and joining the
// two into Relationship values the indexer can turn into IMPLEMENTS edges.
//
// This mirrors symbolindex.go's design (buildSymbolLookup /
// parseGoSymbolDescriptor / ResolveImplementations) but for a different
// symbol grammar and a different resolver process (an external Node.js
// script instead of an in-process go/types pass, since there is no
// TypeScript type checker available from Go).
package resolve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TSRelationship is a single entry in resolve.mjs's "relationships" array —
// see tools/ts-resolver/resolve.mjs's TSRelationship jsdoc typedef for the
// authoritative field meanings. File paths are POSIX-style, relative to the
// project root, matching what scip-typescript descriptors contain.
type TSRelationship struct {
	FromFile   string `json:"fromFile"`
	FromType   string `json:"fromType"`
	FromMethod string `json:"fromMethod"`
	ToFile     string `json:"toFile"`
	ToType     string `json:"toType"`
	ToMethod   string `json:"toMethod"`
}

// TSResolverStats mirrors resolve.mjs's "stats" object.
type TSResolverStats struct {
	Interfaces             int  `json:"interfaces"`
	Classes                int  `json:"classes"`
	PairsChecked           int  `json:"pairsChecked"`
	TypeLevel              int  `json:"typeLevel"`
	MethodLevel            int  `json:"methodLevel"`
	SkippedEmptyInterfaces int  `json:"skippedEmptyInterfaces"`
	// SkippedNoRequiredCallable counts interfaces skipped because they have
	// no required function-typed member — all-optional option bags and pure
	// data shapes are universally (or near-universally) assignable and carry
	// no call-resolution signal.
	SkippedNoRequiredCallable int `json:"skippedNoRequiredCallable"`
	CapExceeded            bool `json:"capExceeded"`
}

// TSResolverOutput is the full JSON document written by resolve.mjs to its
// --out path.
type TSResolverOutput struct {
	Resolver      string           `json:"resolver"`
	TSVersion     string           `json:"tsVersion"`
	Relationships []TSRelationship `json:"relationships"`
	Stats         TSResolverStats  `json:"stats"`
}

// ParseTSResolverOutput unmarshals resolve.mjs's JSON output.
func ParseTSResolverOutput(jsonBytes []byte) (*TSResolverOutput, error) {
	var out TSResolverOutput
	if err := json.Unmarshal(jsonBytes, &out); err != nil {
		return nil, fmt.Errorf("parse ts-resolver output: %w", err)
	}
	return &out, nil
}

// tsSymbolDescriptor is the normalized (relFile, typeName, memberName) view
// of a scip-typescript symbol string, analogous to symbolDescriptor in
// symbolindex.go. relFile is POSIX-style, relative to the project root —
// the same convention resolve.mjs uses for fromFile/toFile, which is what
// makes the two joinable.
type tsSymbolDescriptor struct {
	relFile string
	typ     string
	member  string
}

// tsSymbolLookup maps normalized descriptors to the exact SCIP symbol
// string they came from, mirroring symbolLookup in symbolindex.go.
type tsSymbolLookup struct {
	byMember map[tsSymbolDescriptor]string // (file, type, member) -> full symbol string
	byType   map[tsSymbolDescriptor]string // (file, type, "") -> full symbol string
}

func tsMemberKey(relFile, typ, member string) tsSymbolDescriptor {
	return tsSymbolDescriptor{relFile: relFile, typ: typ, member: member}
}

func tsTypeKey(relFile, typ string) tsSymbolDescriptor {
	return tsSymbolDescriptor{relFile: relFile, typ: typ}
}

// buildTSSymbolLookup parses every symbol string in knownSymbols and
// indexes the ones that look like scip-typescript type or type-member
// descriptors. Symbols that don't parse as such (packages, free functions,
// locals, other languages, malformed strings, or multi-container nested
// descriptors — see parseTSSymbolDescriptor) are silently skipped.
func buildTSSymbolLookup(knownSymbols []string) *tsSymbolLookup {
	lk := &tsSymbolLookup{
		byMember: make(map[tsSymbolDescriptor]string),
		byType:   make(map[tsSymbolDescriptor]string),
	}

	for _, sym := range knownSymbols {
		relFile, typ, member, ok := parseTSSymbolDescriptor(sym)
		if !ok {
			continue
		}
		if member != "" {
			lk.byMember[tsMemberKey(relFile, typ, member)] = sym
		} else if typ != "" {
			lk.byType[tsTypeKey(relFile, typ)] = sym
		}
	}

	return lk
}

// parseTSSymbolDescriptor parses a scip-typescript symbol string of the
// form:
//
//	scip-typescript npm <manager-or-pkg> <version> <dirPath>/`<fileName>`/Type#Method().
//	scip-typescript npm <manager-or-pkg> <version> <dirPath>/`<fileName>`/Type#Method.       (property/abstract member)
//	scip-typescript npm <manager-or-pkg> <version> `<fileName>`/Type#                        (type-level, file at project root: dirPath is empty)
//
// and returns (relFile, typeName, memberName, ok).
//
// Real observed shapes (from a live NestJS graph):
//
//	scip-typescript npm dough-gateway 0.1.0 src/dough/`dough.client.ts`/DoughHttpClient#getWorkload().
//	scip-typescript npm tiny-ts 1.0.0 src/`logger.ts`/Logger#
//
// The descriptor after the "<scheme> <manager> <pkg> <version> " prefix is
// <dirPath>/`<fileName>`/Type#Method(). — the file name is backtick-quoted,
// the leading directory path is not. dirPath may be empty when the file
// lives at the project root (just `file.ts`/...). relFile is reconstructed
// as dirPath + fileName (POSIX-joined), which is exactly the fromFile/toFile
// convention resolve.mjs emits.
//
// Nested containers (e.g. `file.ts`/Outer#Inner#method().) are handled
// defensively: this parser takes the FIRST '#'-segment as the type and the
// LAST segment as the member, but only when there are exactly two segments
// (Type#Member). Three or more segments (an intermediate container) are
// reported as unjoinable (ok=false) rather than mis-joined, since resolve.mjs
// never emits container-qualified fromType/toType and a wrong join would
// silently attach relationships to the wrong node.
func parseTSSymbolDescriptor(sym string) (relFile, typ, member string, ok bool) {
	parts := strings.SplitN(sym, " ", 5)
	if len(parts) != 5 {
		return "", "", "", false
	}
	descriptor := parts[4]

	backtickStart := strings.Index(descriptor, "`")
	if backtickStart < 0 {
		return "", "", "", false // no quoted filename at all: not a file-scoped descriptor
	}
	backtickEnd := strings.Index(descriptor[backtickStart+1:], "`")
	if backtickEnd < 0 {
		return "", "", "", false
	}
	backtickEnd = backtickStart + 1 + backtickEnd

	dirPath := descriptor[:backtickStart]
	fileName := descriptor[backtickStart+1 : backtickEnd]
	if fileName == "" {
		return "", "", "", false
	}
	relFile = dirPath + fileName

	rest := descriptor[backtickEnd+1:]
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return "", "", "", false // bare file symbol, e.g. `file.ts`/
	}

	segments, member, ok := splitTSDescriptorSegments(rest)
	if !ok {
		return "", "", "", false
	}
	switch len(segments) {
	case 0:
		return "", "", "", false
	case 1:
		typ = segments[0]
		if typ == "" {
			return "", "", "", false
		}
		return relFile, typ, member, true
	default:
		// Nested container (Outer#Inner#...): first segment is the type,
		// but an intermediate container means we cannot tell which level
		// resolve.mjs actually meant to join against. Rather than
		// mis-joining to the outer container, treat as unjoinable.
		return "", "", "", false
	}
}

// splitTSDescriptorSegments splits the '#'-delimited descriptor tail (after
// the file segment) into its container-name segments plus the trailing
// member name, applying the same trailing-shape rules
// parseGoSymbolDescriptor uses:
//
//	Type#                    (type-level: member == "")
//	Type#Method().           (concrete method)
//	Type#Method.             (property / abstract member)
//
// For a single-level descriptor (Type#Member...), segments has length 1
// (["Type"]) and member is the parsed method/property name (or "" for
// type-level). For a nested descriptor (Outer#Inner#Member...), segments has
// length 2 (["Outer", "Inner"]) so the caller can detect and reject it.
func splitTSDescriptorSegments(rest string) (segments []string, member string, ok bool) {
	hashIdx := strings.Index(rest, "#")
	if hashIdx < 0 {
		return nil, "", false // no '#': not a type/member descriptor
	}
	head := rest[:hashIdx]
	if head == "" {
		return nil, "", false
	}
	tail := rest[hashIdx+1:]

	// If the tail itself contains another '#', this is a nested container
	// (Outer#Inner#...). Recurse to find the ultimate member shape but
	// report two segments so the caller rejects the join.
	if nextHash := strings.Index(tail, "#"); nextHash >= 0 {
		innerSegments, innerMember, innerOK := splitTSDescriptorSegments(tail)
		if !innerOK {
			return nil, "", false
		}
		return append([]string{head}, innerSegments...), innerMember, true
	}

	switch {
	case tail == "":
		return []string{head}, "", true // type-level: `file`/Type#
	case strings.HasSuffix(tail, "()."):
		return []string{head}, strings.TrimSuffix(tail, "()."), true
	case strings.HasSuffix(tail, "()"):
		return []string{head}, strings.TrimSuffix(tail, "()"), true
	case strings.HasSuffix(tail, "."):
		m := strings.TrimSuffix(tail, ".")
		if m == "" {
			return nil, "", false
		}
		return []string{head}, m, true
	default:
		return nil, "", false
	}
}

// TSJoinStats reports what happened while joining resolve.mjs's output onto
// known SCIP symbol strings, mirroring the type-level/method-level/dropped
// counters in Stats.
type TSJoinStats struct {
	TypeLevelEmitted     int
	MethodLevelEmitted   int
	DroppedMissingSymbol int
}

// JoinTSRelationships joins resolve.mjs's parsed output onto the SCIP
// symbol strings actually present in knownSymbols (i.e. what scip-typescript
// already emitted into index.scip for this project). Only relationships
// where BOTH endpoints resolve to a known symbol are returned; everything
// else is counted in TSJoinStats.DroppedMissingSymbol.
//
// Per the brief, pairs already connected via explicit heritage are NOT
// filtered out here — the caller (scip_indexer.go) dedupes against
// SCIP-native relationships the same way the Go resolver's caller does.
func JoinTSRelationships(parsed *TSResolverOutput, knownSymbols []string) ([]Relationship, TSJoinStats) {
	var stats TSJoinStats
	if parsed == nil {
		return nil, stats
	}

	lookup := buildTSSymbolLookup(knownSymbols)

	rels := make([]Relationship, 0, len(parsed.Relationships))
	for _, r := range parsed.Relationships {
		var fromSym, toSym string
		var ok bool

		if r.FromMethod == "" && r.ToMethod == "" {
			fromSym, ok = lookup.byType[tsTypeKey(r.FromFile, r.FromType)]
			if !ok {
				stats.DroppedMissingSymbol++
				continue
			}
			toSym, ok = lookup.byType[tsTypeKey(r.ToFile, r.ToType)]
			if !ok {
				stats.DroppedMissingSymbol++
				continue
			}
			rels = append(rels, Relationship{FromSymbol: fromSym, ToSymbol: toSym, IsImplementation: true})
			stats.TypeLevelEmitted++
			continue
		}

		fromSym, ok = lookup.byMember[tsMemberKey(r.FromFile, r.FromType, r.FromMethod)]
		if !ok {
			stats.DroppedMissingSymbol++
			continue
		}
		toSym, ok = lookup.byMember[tsMemberKey(r.ToFile, r.ToType, r.ToMethod)]
		if !ok {
			stats.DroppedMissingSymbol++
			continue
		}
		rels = append(rels, Relationship{FromSymbol: fromSym, ToSymbol: toSym, IsImplementation: true})
		stats.MethodLevelEmitted++
	}

	return rels, stats
}

// Sentinel errors distinguishing "environment missing, skip with warning"
// conditions from real resolver failures. Callers (scip_indexer.go) should
// treat errors satisfying errors.Is(err, ErrTSResolverEnvironmentMissing) as
// best-effort skips, exactly like a Go packages.Load failure is handled.
var (
	// ErrTSResolverEnvironmentMissing wraps exit code 2 (no `typescript`
	// resolvable in the target project) and the "node not in PATH" /
	// "script not found" cases the indexer checks before even invoking the
	// script.
	ErrTSResolverEnvironmentMissing = errors.New("ts-resolver environment missing")
	// ErrTSResolverVersionTooOld wraps exit code 3 (typescript too old to
	// expose checker.isTypeAssignableTo).
	ErrTSResolverVersionTooOld = errors.New("ts-resolver: typescript version too old")
)

// RunTSResolver executes `node <scriptPath> --project <projectRoot> --out
// <tmpfile>` with the given timeout, then parses and returns its output.
//
// Exit code contract (see tools/ts-resolver/resolve.mjs's header comment):
//
//	0  success -> parse --out and return it
//	1  usage error -> real failure (should not happen; args are controlled here)
//	2  typescript module not found -> ErrTSResolverEnvironmentMissing (skip, warn)
//	3  typescript version too old -> ErrTSResolverVersionTooOld (skip, warn)
//	4  unexpected internal failure -> real failure, returned as-is
//
// This function does not itself check for `node` in PATH or resolve the
// script path — that is the caller's (scip_indexer.go's) responsibility, so
// it can produce a clearer warning before ever shelling out.
func RunTSResolver(ctx context.Context, scriptPath, projectRoot string, timeout time.Duration) (*TSResolverOutput, error) {
	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	outFile, err := os.CreateTemp("", "ts-resolver-*.json")
	if err != nil {
		return nil, fmt.Errorf("create temp output file: %w", err)
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	absProjectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		absProjectRoot = projectRoot
	}

	cmd := exec.CommandContext(runCtx, "node", scriptPath, "--project", absProjectRoot, "--out", outPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	if runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("ts-resolver timed out after %s running %s: %w", timeout, scriptPath, runCtx.Err())
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code := exitErr.ExitCode()
			switch code {
			case 2:
				return nil, fmt.Errorf("%w: %s", ErrTSResolverEnvironmentMissing, strings.TrimSpace(stderr.String()))
			case 3:
				return nil, fmt.Errorf("%w: %s", ErrTSResolverVersionTooOld, strings.TrimSpace(stderr.String()))
			default:
				return nil, fmt.Errorf("ts-resolver exited %d: %s", code, strings.TrimSpace(stderr.String()))
			}
		}
		return nil, fmt.Errorf("ts-resolver failed to run: %w (stderr: %s)", runErr, strings.TrimSpace(stderr.String()))
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read ts-resolver output %s: %w", outPath, err)
	}
	return ParseTSResolverOutput(data)
}
