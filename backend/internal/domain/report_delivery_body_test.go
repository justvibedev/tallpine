package domain

import "testing"

func TestCoordinationDeliveryBody(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		ok             bool
	}{
		{"report", WrapReportDelivery("r-1", "[report] done\nsecond line"), "[report] done\nsecond line", true},
		{"session", WrapSessionDelivery("d-1", "please rebase"), "please rebase", true},
		{"padded", "  " + WrapSessionDelivery("d-1", " x ") + "\n", "x", true},
		{"handoff", `<ao-handoff-request switch-id="x">note</ao-handoff-request>`, "", false},
		{"plain", "fix the importer", "", false},
	} {
		got, ok := CoordinationDeliveryBody(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("%s: got (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}
