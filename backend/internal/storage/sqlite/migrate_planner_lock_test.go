package sqlite

import (
	"database/sql"
	"os"
	"strings"
	"testing"
)

func plannerLockMigrationUp(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("migrations/20261104110000000_planner_lock_to_default.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw)
	return up[strings.Index(up, "-- +goose Up")+len("-- +goose Up") : strings.Index(up, "-- +goose Down")]
}

func appPolicyAndDefaults(t *testing.T, db *sql.DB) (policy, defaults string) {
	t.Helper()
	if err := db.QueryRow(`SELECT agent_policy, workflow_defaults FROM app_settings WHERE id = 1`).Scan(&policy, &defaults); err != nil {
		t.Fatal(err)
	}
	return policy, defaults
}

func TestPlannerLockMigration(t *testing.T) {
	db := openReviewSettingsDB(t)
	mustExec(t, db, `UPDATE app_settings SET agent_policy = ? WHERE id = 1`,
		`{"version":2,"rules":{"planner":{"mode":"always","autoApprove":"off","harness":"codex"},"other":{"x":"y"}}}`)
	seedProject(t, db, "on", `{"planAutoApprove":true,"planningMode":"off"}`)
	seedProject(t, db, "unset", `{"planningMode":"auto"}`)
	seedProject(t, db, "bare", nil)
	seedProject(t, db, "eval", `{"eval":true,"sessionPrefix":"e"}`)
	up := plannerLockMigrationUp(t)
	mustExec(t, db, up)

	if got := reviewSettingsProjectConfig(t, db, "on"); got["planAutoApprove"] != false || got["planningMode"] != "off" {
		t.Fatalf("on project = %v, want planAutoApprove false and planningMode untouched", got)
	}
	if got := reviewSettingsProjectConfig(t, db, "unset"); got["planningMode"] != "auto" || got["planAutoApprove"] != nil {
		t.Fatalf("unset project = %v, want untouched", got)
	}
	if got := reviewSettingsProjectConfig(t, db, "eval"); got["eval"] != true {
		t.Fatalf("eval project = %v, want eval left alone", got)
	}
	policy, defaults := appPolicyAndDefaults(t, db)
	if !strings.Contains(defaults, `"evalWaiverClosed":true`) {
		t.Fatalf("defaults = %s, want the eval waiver gate closed", defaults)
	}
	if !strings.Contains(defaults, `"planningMode":"always"`) {
		t.Fatalf("defaults = %s, want the user mode copied", defaults)
	}
	if strings.Contains(defaults, "planAutoApprove") {
		t.Fatalf("defaults = %s, migration must not enable or set auto-approval", defaults)
	}
	if strings.Contains(policy, `"mode"`) || strings.Contains(policy, `"autoApprove"`) || !strings.Contains(policy, `"harness":"codex"`) || !strings.Contains(policy, `"other"`) {
		t.Fatalf("policy = %s, want only mode and autoApprove removed", policy)
	}

	// A rerun changes nothing.
	mustExec(t, db, up)
	policy2, defaults2 := appPolicyAndDefaults(t, db)
	if policy2 != policy || defaults2 != defaults {
		t.Fatalf("rerun changed state: %s %s", policy2, defaults2)
	}
}

// Without a user autoApprove "off", projects that turned it on keep it.
func TestPlannerLockMigrationLeavesOnProjectsWithoutLock(t *testing.T) {
	db := openReviewSettingsDB(t)
	mustExec(t, db, `UPDATE app_settings SET agent_policy = ? WHERE id = 1`, `{"version":2,"rules":{"planner":{"mode":"off"}}}`)
	seedProject(t, db, "on", `{"planAutoApprove":true}`)
	seedProject(t, db, "eval", `{"eval":true}`)
	mustExec(t, db, plannerLockMigrationUp(t))
	if _, defaults := appPolicyAndDefaults(t, db); strings.Contains(defaults, "evalWaiverClosed") {
		t.Fatalf("defaults = %s, no lock means the waiver gate stays open", defaults)
	}
	if got := reviewSettingsProjectConfig(t, db, "on"); got["planAutoApprove"] != true {
		t.Fatalf("project = %v, want planAutoApprove kept", got)
	}
	if _, defaults := appPolicyAndDefaults(t, db); !strings.Contains(defaults, `"planningMode":"off"`) {
		t.Fatalf("defaults = %s", defaults)
	}
}

// An invalid or flat v1 document is left alone and the migration still succeeds.
func TestPlannerLockMigrationSafeOnOtherDocs(t *testing.T) {
	for name, doc := range map[string]string{
		"invalid": `not json`,
		"v1":      `{"maxPermission":"auto","plannerMode":"always"}`,
		"empty":   `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			db := openReviewSettingsDB(t)
			if name == "invalid" {
				// The column may be CHECKed; an invalid document cannot exist then.
				if _, err := db.Exec(`UPDATE app_settings SET agent_policy = ? WHERE id = 1`, doc); err != nil {
					t.Skip("column rejects invalid JSON")
				}
			} else {
				mustExec(t, db, `UPDATE app_settings SET agent_policy = ? WHERE id = 1`, doc)
			}
			seedProject(t, db, "on", `{"planAutoApprove":true}`)
			mustExec(t, db, plannerLockMigrationUp(t))
			policy, defaults := appPolicyAndDefaults(t, db)
			if policy != doc || strings.Contains(defaults, "planningMode") {
				t.Fatalf("policy = %s defaults = %s, want both untouched", policy, defaults)
			}
			if got := reviewSettingsProjectConfig(t, db, "on"); got["planAutoApprove"] != true {
				t.Fatalf("project = %v", got)
			}
		})
	}
}
