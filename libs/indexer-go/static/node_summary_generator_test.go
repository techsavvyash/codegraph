package static

import (
	"testing"
)

func TestGenerateNodeSummary_docstring(t *testing.T) {
	in := NodeSummaryInput{
		Name:      "CreatePayout",
		Docstring: "CreatePayout validates and persists a new payout record.",
	}
	got := GenerateNodeSummary(in)
	want := "Validates and persists a new payout record"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateNodeSummary_docstringNoPrefix(t *testing.T) {
	in := NodeSummaryInput{
		Name:      "Validate",
		Docstring: "Ensures the request fields satisfy business rules.",
	}
	got := GenerateNodeSummary(in)
	want := "Ensures the request fields satisfy business rules"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateNodeSummary_verbPrefix(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"GetPayoutByID", "Retrieves payout by ID"},
		{"ListTransactions", "Lists transactions"},
		{"CreateAccount", "Creates account"},
		{"DeleteUser", "Deletes user"},
		{"UpdateBalance", "Updates balance"},
		{"ValidateRequest", "Validates request"},
		{"SendNotification", "Sends notification"},
		{"ParseResponse", "Parses response"},
		{"IsActive", "Reports whether active"},
		{"NewClient", "Constructs client"},
		{"BuildPayload", "Builds payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GenerateNodeSummary(NodeSummaryInput{Name: tc.name})
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGenerateNodeSummary_rpcHandler(t *testing.T) {
	in := NodeSummaryInput{
		Name:         "FundPayout",
		IsRPCHandler: true,
	}
	got := GenerateNodeSummary(in)
	want := "Handles RPC request: fund payout"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateNodeSummary_noMatch(t *testing.T) {
	in := NodeSummaryInput{Name: "FundPayout"}
	got := GenerateNodeSummary(in)
	// No prefix match → fallback split: "Fund" + "Payout"
	want := "Funds payout"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateNodeSummary_stripsSCIPDescriptor(t *testing.T) {
	// SCIP leaks "()." into method names; it must not appear in the summary.
	got := GenerateNodeSummary(NodeSummaryInput{Name: "UpsertPayoutError()."})
	want := "Upserts payout error"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateNodeSummary_emptyObjectFromEffects(t *testing.T) {
	// A bare "Upsert" method with no object word should borrow the written table
	// instead of emitting the old broken "Upserts ()".
	in := NodeSummaryInput{
		Name:    "Upsert().",
		Effects: NodeEffects{DBWrites: []string{"lookup_payout_error"}},
	}
	got := GenerateNodeSummary(in)
	// The borrowed object is no longer suppressed from the effect clause (dedup
	// applies only to docstring prose), so the write is stated explicitly — which
	// also adds the read-vs-write signal the bare lead lacks.
	want := "Upserts lookup_payout_error — writes `lookup_payout_error`"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateNodeSummary_effectsAppended(t *testing.T) {
	in := NodeSummaryInput{
		Name:         "UpsertPayoutError",
		IsRPCHandler: true,
		Effects: NodeEffects{
			DBWrites: []string{"lookup_payout_error"},
			Calls:    []string{"sol.UpsertPayoutPSPError"},
		},
	}
	got := GenerateNodeSummary(in)
	// calls lead the effect clause (they are the navigation signal), then writes.
	want := "Handles RPC request: upsert payout error — calls `sol.UpsertPayoutPSPError`, writes `lookup_payout_error`"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateNodeSummary_dedupesEffectInLead(t *testing.T) {
	// The docstring already names the table; it must not be echoed in the effects.
	in := NodeSummaryInput{
		Name:      "Upsert",
		Docstring: "Upsert writes a row into lookup_payout_error.",
		Effects:   NodeEffects{DBWrites: []string{"lookup_payout_error"}},
	}
	got := GenerateNodeSummary(in)
	want := "Writes a row into lookup_payout_error"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateNodeSummary_readVsWrite(t *testing.T) {
	in := NodeSummaryInput{
		Name:    "ProcessSettlement",
		Effects: NodeEffects{DBWrites: []string{"payouts"}, DBReads: []string{"ledger"}},
	}
	got := GenerateNodeSummary(in)
	want := "Processes settlement — writes `payouts`, reads `ledger`"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateNodeSummary_argsClauseAndCallees(t *testing.T) {
	in := NodeSummaryInput{
		Name:         "FundPayout",
		IsRPCHandler: true,
		GoSignature:  "func (s *Server) FundPayout(ctx context.Context, req *pb.FundPayoutRequest) (*pb.FundPayoutResponse, error)",
		Effects: NodeEffects{
			Functions: []string{"prepareBeneficiary", "computeBalanceImpact", "UpdatePayoutStatus"},
			DBWrites:  []string{"payout"},
			Events:    []string{"PayoutFunded"},
		},
	}
	got := GenerateNodeSummary(in)
	want := "Handles RPC request: fund payout (FundPayoutRequest→FundPayoutResponse) — " +
		"calls `prepareBeneficiary`, `computeBalanceImpact`, `UpdatePayoutStatus`, writes `payout`, emits `PayoutFunded`"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestSplitSignature_args(t *testing.T) {
	cases := []struct{ sig, req, resp string }{
		{"func (s *Server) FundPayout(ctx context.Context, req *pb.FundPayoutRequest) (*pb.FundPayoutResponse, error)", "FundPayoutRequest", "FundPayoutResponse"},
		{"func GetPayout(ctx context.Context, id string) (*Payout, error)", "", "Payout"}, // id string is uninteresting → no req; resp Payout
		{"func helper(a int, b int) bool", "", ""},                                        // no meaningful types
		{"not a signature", "", ""},
	}
	for _, c := range cases {
		p, r := splitSignature(c.sig)
		gotReq, gotResp := firstMeaningfulType(p), firstMeaningfulType(r)
		if gotReq != c.req || gotResp != c.resp {
			t.Errorf("sig %q: got (%q,%q) want (%q,%q)", c.sig, gotReq, gotResp, c.req, c.resp)
		}
	}
}

// TestNormalizeIdent pins the canonical-name contract that scip_indexer relies on
// at ingest: SCIP display names for functions/methods carry a descriptor suffix
// ("EditSettlement().") which must be stripped so `name` is a clean identifier.
// If this breaks, node names revert to carrying "()." and every exact-match query
// (analyze_function, rpc_anatomy) silently returns "not found" again.
func TestNormalizeIdent(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"EditSettlement().", "EditSettlement"},   // method descriptor
		{"UpdateMerchantBank().", "UpdateMerchantBank"},
		{"Upsert()", "Upsert"},                    // no trailing dot
		{"BankModel#Save().", "BankModel"},        // '#' member separator: everything after '#' dropped
		{"AlreadyClean", "AlreadyClean"},          // no-op on clean names
		{"  Padded().  ", "Padded"},               // surrounding whitespace
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := normalizeIdent(tc.in); got != tc.want {
				t.Errorf("normalizeIdent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestGenerateNodeSummary_setupNotShadowedBySet(t *testing.T) {
	// Longest-prefix-first ordering: "Setup" must win over "Set".
	got := GenerateNodeSummary(NodeSummaryInput{Name: "SetupRoutes"})
	want := "Sets up routes"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateNodeSummary_unexportedVerbMatch(t *testing.T) {
	// Unexported (lowercase-first) helpers must match the same verb rules as
	// exported ones, instead of producing "Processs".
	cases := []struct{ name, want string }{
		{"processFundingAction", "Processes funding action"},
		{"getPayoutData", "Retrieves payout data"},
		{"validateBalance", "Validates balance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GenerateNodeSummary(NodeSummaryInput{Name: tc.name}); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGenerateNodeSummary_verbBoundary(t *testing.T) {
	// "Settle" must not be split into the "Set" verb + "tle".
	got := GenerateNodeSummary(NodeSummaryInput{Name: "SettleAccount"})
	want := "Settles account"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSplitCamel(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"GetPayoutByID", []string{"Get", "Payout", "By", "ID"}},
		{"HTTPClient", []string{"HTTP", "Client"}},
		{"newRouter", []string{"new", "Router"}},
		{"ID", []string{"ID"}},
		{"simple", []string{"simple"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := splitCamel(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("token[%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
