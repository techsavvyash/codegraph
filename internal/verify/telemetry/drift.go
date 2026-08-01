package telemetry

import "fmt"

// driftThreshold is the fractional change (either direction) on a numeric
// counter that is reported as drift. RFC-013 Layer 3 default: ±25%.
const driftThreshold = 0.25

// diffRuns is a pure function comparing two RunRecords and returning the
// drift entries between them. No graph I/O — kept separate so it can be
// unit-tested exhaustively.
func diffRuns(previous, current *RunRecord) []Drift {
	if previous == nil || current == nil {
		return nil
	}

	var drifts []Drift

	numeric := []struct {
		name      string
		prev, cur int64
	}{
		{name: "files", prev: previous.Files, cur: current.Files},
		{name: "functions", prev: previous.Functions, cur: current.Functions},
		{name: "methods", prev: previous.Methods, cur: current.Methods},
		{name: "symbols", prev: previous.Symbols, cur: current.Symbols},
		{name: "callsEdges", prev: previous.CallsEdges, cur: current.CallsEdges},
		{name: "usesValueEdges", prev: previous.UsesValueEdges, cur: current.UsesValueEdges},
		{name: "implementsEdges", prev: previous.ImplementsEdges, cur: current.ImplementsEdges},
		{name: "apiRoutes", prev: previous.APIRoutes, cur: current.APIRoutes},
		{name: "promotedFunctions", prev: previous.PromotedFunctions, cur: current.PromotedFunctions},
		{name: "decoratedFunctions", prev: previous.DecoratedFunctions, cur: current.DecoratedFunctions},
	}
	for _, e := range numeric {
		if d, warn := fractionalDrift(float64(e.prev), float64(e.cur)); warn {
			drifts = append(drifts, Drift{
				Counter:  e.name,
				Previous: float64(e.prev),
				Current:  float64(e.cur),
				Detail:   d,
			})
		}
	}

	if d, warn := fractionalDrift(previous.CallsPerFunction, current.CallsPerFunction); warn && current.CallsPerFunction < previous.CallsPerFunction {
		// callsPerFunction is only flagged on a *drop* per RFC-013 ("drop
		// >25%") — a rise is not itself a correctness signal the way a
		// collapse in fan-out is.
		drifts = append(drifts, Drift{
			Counter:  "callsPerFunction",
			Previous: previous.CallsPerFunction,
			Current:  current.CallsPerFunction,
			Detail:   d,
		})
	}

	drifts = append(drifts, diffDist("rangeSourceDist", previous.RangeSourceDist, current.RangeSourceDist)...)
	drifts = append(drifts, diffDist("detectionSourceDist", previous.DetectionSourceDist, current.DetectionSourceDist)...)

	return drifts
}

// fractionalDrift reports whether the change from prev to cur crosses
// driftThreshold, and a human-readable detail string. Zero baselines are
// handled explicitly: 0→N (N>0) and N→0 (N>0) always warn (infinite/total
// relative change) without dividing by zero; 0→0 never warns.
func fractionalDrift(prev, cur float64) (detail string, warn bool) {
	if prev == 0 && cur == 0 {
		return "", false
	}
	if prev == 0 {
		return fmt.Sprintf("0 → %.4g (new)", cur), true
	}
	if cur == 0 {
		return fmt.Sprintf("%.4g → 0 (dropped to zero)", prev), true
	}
	frac := (cur - prev) / prev
	if frac < 0 {
		frac = -frac
	}
	if frac > driftThreshold {
		return fmt.Sprintf("%+.1f%% change", (cur-prev)/prev*100), true
	}
	return "", false
}

// diffDist reports drift for one distribution: any counter-value change
// beyond threshold on a shared key (zero-baseline rules from
// fractionalDrift apply), plus any key appearing or disappearing entirely.
func diffDist(name string, prev, cur map[string]int64) []Drift {
	var drifts []Drift

	keys := make(map[string]struct{}, len(prev)+len(cur))
	for k := range prev {
		keys[k] = struct{}{}
	}
	for k := range cur {
		keys[k] = struct{}{}
	}

	for k := range keys {
		pv, pok := prev[k]
		cv, cok := cur[k]
		switch {
		case pok && !cok:
			drifts = append(drifts, Drift{
				Counter:  fmt.Sprintf("%s[%s]", name, k),
				Previous: float64(pv),
				Current:  0,
				Detail:   "key disappeared",
			})
		case !pok && cok:
			drifts = append(drifts, Drift{
				Counter:  fmt.Sprintf("%s[%s]", name, k),
				Previous: 0,
				Current:  float64(cv),
				Detail:   "key appeared",
			})
		default:
			if d, warn := fractionalDrift(float64(pv), float64(cv)); warn {
				drifts = append(drifts, Drift{
					Counter:  fmt.Sprintf("%s[%s]", name, k),
					Previous: float64(pv),
					Current:  float64(cv),
					Detail:   d,
				})
			}
		}
	}

	return drifts
}
