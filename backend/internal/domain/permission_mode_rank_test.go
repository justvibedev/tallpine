package domain

import "testing"

func TestPermissionModeRankIsStrictlyOrdered(t *testing.T) {
	ordered := []PermissionMode{PermissionModeDefault, PermissionModeAcceptEdits, PermissionModeAuto, PermissionModeBypassPermissions}
	for i := 1; i < len(ordered); i++ {
		if ordered[i-1].Rank() >= ordered[i].Rank() {
			t.Fatalf("%s (%d) must rank below %s (%d)", ordered[i-1], ordered[i-1].Rank(), ordered[i], ordered[i].Rank())
		}
	}
	if PermissionMode("").Rank() != 0 || PermissionMode("bogus").Rank() != 0 {
		t.Fatal("empty and unknown modes must rank as default")
	}
}
