package static

import "testing"

// ── P0-7: contested segment aliases must never bind nondeterministically ──────

// fleetLikeServices mirrors the live-workspace collision set: settlement vs
// settlement-orchestration ("settlement"), two orchestrations ("orchestration"),
// two dashboards ("dashboard").
var fleetLikeServices = [][3]string{ // {name, packageName, id}
	{"settlement", "", "id-settlement"},
	{"settlement-orchestration", "", "id-so"},
	{"liquidity-orchestration", "", "id-lo"},
	{"ops-dashboard", "", "id-ops"},
	{"mp-dashboard", "", "id-mp"},
}

func buildTestServiceIndex(services [][3]string) *serviceIndex {
	b := newServiceIndexBuilder()
	for _, s := range services {
		b.addService(s[0], s[1], s[2])
	}
	return b.build()
}

func TestServiceIndex_ContestedAliases_BothLoadOrders(t *testing.T) {
	forward := fleetLikeServices
	reverse := make([][3]string, len(forward))
	for i, s := range forward {
		reverse[len(forward)-1-i] = s
	}

	for _, order := range [][][3]string{forward, reverse} {
		si := buildTestServiceIndex(order)

		// Full-name primaries always win over another service's segment claim.
		if got := si.resolveByName("settlement"); got != "id-settlement" {
			t.Errorf("resolveByName(settlement) = %q, want id-settlement", got)
		}
		if got := si.resolveByName("settlement-orchestration"); got != "id-so" {
			t.Errorf("resolveByName(settlement-orchestration) = %q, want id-so", got)
		}
		if got := si.resolveByName("ops-dashboard"); got != "id-ops" {
			t.Errorf("resolveByName(ops-dashboard) = %q, want id-ops", got)
		}

		// Contested segments resolve to nothing — no edge beats a wrong edge.
		if got := si.resolveByName("orchestration"); got != "" {
			t.Errorf("resolveByName(orchestration) = %q, want \"\" (contested)", got)
		}
		if got := si.resolveByName("dashboard"); got != "" {
			t.Errorf("resolveByName(dashboard) = %q, want \"\" (contested)", got)
		}

		// An uncontested segment keeps its sole claimant.
		if got := si.resolveByName("liquidity"); got != "id-lo" {
			t.Errorf("resolveByName(liquidity) = %q, want id-lo", got)
		}
	}
}

func TestServiceIndex_QuerySideSegmentsCannotPreempt(t *testing.T) {
	// resolveByName must try the query's own full/canonical forms before its
	// segments: even with "settlement" present in byName, a lookup for
	// "settlement-orchestration" must never return the settlement service.
	si := buildTestServiceIndex(fleetLikeServices)
	for range 20 { // the old map-ordered candidate list failed this intermittently
		if got := si.resolveByName("settlement-orchestration"); got != "id-so" {
			t.Fatalf("resolveByName(settlement-orchestration) = %q, want id-so", got)
		}
	}
}

func TestServiceIndex_PrimaryConflict_Dropped(t *testing.T) {
	// The round-1 DocumentService collision: the same proto service name declared
	// by two repos is ambiguous even as a primary alias — it must resolve to
	// nothing (the authoritative getter table supplies the real owner instead).
	b := newServiceIndexBuilder()
	b.addService("account", "", "id-account")
	b.addService("payment", "", "id-payment")
	b.addProtoContract("account.grpc.v1", "DocumentService", "id-account")
	b.addProtoContract("payment.grpc.v1", "DocumentService", "id-payment")
	si := b.build()

	if got := si.resolveByName("DocumentService"); got != "" {
		t.Errorf("resolveByName(DocumentService) = %q, want \"\" (primary conflict)", got)
	}
	if got := si.resolveByProto("account.grpc.v1"); got != "id-account" {
		t.Errorf("resolveByProto(account.grpc.v1) = %q, want id-account", got)
	}
	// Universal proto-package segments are contested across services.
	if got := si.resolveByProto("grpc"); got != "" {
		t.Errorf("resolveByProto(grpc) = %q, want \"\" (contested)", got)
	}
	if got := si.resolveByProto("v1"); got != "" {
		t.Errorf("resolveByProto(v1) = %q, want \"\" (contested)", got)
	}
}
