package sqlite

import (
	"os"
	"strings"
	"testing"
)

func TestContextBudgetCheckMigratesToContextHealth(t *testing.T) {
	db := openReviewSettingsDB(t)
	seedProject(t, db, "on", `{"contextBudgetCheck":true,"contextBudgetTokens":9000,"sessionPrefix":"x"}`)
	seedProject(t, db, "off", `{"contextBudgetCheck":false,"sessionPrefix":"y"}`)
	seedProject(t, db, "unset", `{"sessionPrefix":"z"}`)
	seedProject(t, db, "bare", nil)
	raw, err := os.ReadFile("migrations/20261118100000001_context_health_setting.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw)
	up = up[strings.Index(up, "-- +goose Up")+len("-- +goose Up") : strings.Index(up, "-- +goose Down")]
	mustExec(t, db, up)

	if got := reviewSettingsProjectConfig(t, db, "on"); got["contextHealth"] != true || got["contextBudgetCheck"] != nil || got["contextBudgetTokens"] != float64(9000) || got["sessionPrefix"] != "x" {
		t.Fatalf("on project = %v, want contextHealth true and the rest kept", got)
	}
	if got := reviewSettingsProjectConfig(t, db, "off"); got["contextHealth"] != nil || got["contextBudgetCheck"] != nil || got["sessionPrefix"] != "y" {
		t.Fatalf("off project = %v, want the old key dropped and nothing turned on", got)
	}
	if got := reviewSettingsProjectConfig(t, db, "unset"); got["contextHealth"] != nil || got["sessionPrefix"] != "z" {
		t.Fatalf("unset project = %v, want untouched", got)
	}
	mustExec(t, db, up) // a rerun changes nothing
	if got := reviewSettingsProjectConfig(t, db, "on"); got["contextHealth"] != true || got["contextBudgetCheck"] != nil {
		t.Fatalf("after rerun = %v", got)
	}
}
