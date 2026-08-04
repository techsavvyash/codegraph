package telemetry

import "testing"

func TestCallsPerFunction(t *testing.T) {
	cases := []struct {
		name                string
		calls, funcs, meths int64
		want                float64
	}{
		{"normal", 100, 40, 60, 1.0},
		{"zero functions and methods", 0, 0, 0, 0},
		{"calls but no functions/methods", 5, 0, 0, 0},
		{"only functions", 10, 5, 0, 2.0},
		{"only methods", 10, 0, 5, 2.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callsPerFunction(tc.calls, tc.funcs, tc.meths)
			if got != tc.want {
				t.Errorf("callsPerFunction(%d, %d, %d) = %v, want %v", tc.calls, tc.funcs, tc.meths, got, tc.want)
			}
		})
	}
}

func TestRecordIndexRun_RequiresServiceAndScope(t *testing.T) {
	ctx := t.Context()
	if _, err := RecordIndexRun(ctx, nil, "", "main", "t0", "t1"); err == nil {
		t.Fatal("expected error for empty serviceName")
	}
	if _, err := RecordIndexRun(ctx, nil, "svc", "", "t0", "t1"); err == nil {
		t.Fatal("expected error for empty scopeID")
	}
}
