package sqlite

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// The order of migrationHistorySteps is load-bearing: later repairs read the
// ledger earlier ones rewrite. Pin it so a reorder is a deliberate edit.
func TestMigrationHistoryStepsOrder(t *testing.T) {
	want := []struct{ label, fn string }{
		{"repair renumbered agent-install-jobs migration history", "repairRenumberedAgentInstallJobsMigrationHistory"},
		{"repair renumbered provider-switching migration history", "repairRenumberedProviderSwitchingMigrationHistory"},
		{"repair renumbered chat migration history", "repairRenumberedChatMigrationHistory"},
		{"repair renumbered cue migration history", "repairRenumberedCueMigrationHistory"},
		{"repair renumbered task-provisioning migration history", "repairRenumberedTaskProvisioningMigrationHistory"},
		{"repair renumbered usage-cost migration history", "repairRenumberedUsageCostMigrationHistory"},
		{"repair renumbered usage-limit migration history", "repairRenumberedUsageLimitMigrationHistory"},
		{"prepare cancelled conversation-turn migration", "prepareCancelledConversationTurnsMigration"},
		{"repair renumbered Codex profile migration history", "repairRenumberedCodexProfileMigrationHistory"},
		{"repair renumbered agent-inventory migration history", "repairRenumberedAgentInventoryMigrationHistory"},
		{"prepare usage cost migration", "prepareUsageCostMigration"},
		{"prepare auto-inject-review migration", "prepareAutoInjectReviewMigration"},
		{"prepare PR comment review-id migration", "preparePRCommentReviewIDMigration"},
		{"repair renumbered PR review-partial migration history", "repairRenumberedPRReviewPartialMigrationHistory"},
		{"repair burned automations migration history", "repairBurnedAutomationsMigrationHistory"},
		{"prepare burned schema repairs", "prepareBurnedSchemaRepairs"},
		{"prepare review per-harness migration", "prepareReviewPerHarnessMigration"},
		{"prepare browser verifier migration", "prepareBrowserVerifierMigration"},
		{"prepare queued-turn promotion migration", "prepareQueuedTurnPromotionMigration"},
		{"prepare session reviewer agent-config migration", "prepareSessionReviewerAgentConfigMigration"},
	}
	if len(migrationHistorySteps) != len(want) {
		t.Fatalf("steps = %d, want %d", len(migrationHistorySteps), len(want))
	}
	for i, s := range migrationHistorySteps {
		name := runtime.FuncForPC(reflect.ValueOf(s.run).Pointer()).Name()
		if s.label != want[i].label || !strings.HasSuffix(name, "sqlite."+want[i].fn) {
			t.Fatalf("step %d = %q/%s, want %q/%s", i, s.label, name, want[i].label, want[i].fn)
		}
	}
}
