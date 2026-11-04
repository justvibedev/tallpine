package cli

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func runBacklogWithStdin(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out strings.Builder
	cmd := NewRootCommand(Deps{ProcessAlive: func(int) bool { return true }, Out: &out, Err: io.Discard})
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestBacklogDeleteConfirmsThenDeletes(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 200, `{"deleted":["B4","B3"],"skipped":[]}` })

	// Declining sends nothing.
	out, err := runBacklogWithStdin(t, "n\n", "backlog", "delete", "B3")
	if err != nil || !strings.Contains(out, "aborted") || len(*calls) != 0 {
		t.Fatalf("declined: out=%q err=%v calls=%v", out, err, *calls)
	}
	// No stdin and no --yes is a usage error, not a silent delete.
	if _, err := runBacklogWithStdin(t, "", "backlog", "delete", "B3"); err == nil || len(*calls) != 0 {
		t.Fatalf("empty stdin: err=%v calls=%v", err, *calls)
	}
	out, err = runBacklogWithStdin(t, "y\n", "backlog", "delete", "B3")
	if err != nil || !strings.HasSuffix(out, "deleted 2 item(s): B4, B3\n") {
		t.Fatalf("confirmed: out=%q err=%v", out, err)
	}
	if c := (*calls)[0]; c.method != "DELETE" || c.path != "/api/v1/projects/demo/backlog/B3" {
		t.Fatalf("request = %+v", c)
	}
	// --yes skips the prompt; --json prints the response.
	out, err = runBacklog(t, "backlog", "delete", "B3", "--yes", "--json")
	var parsed apitypes.BacklogDeleteResponse
	if err != nil || json.Unmarshal([]byte(out), &parsed) != nil || len(parsed.Deleted) != 2 || len(*calls) != 2 {
		t.Fatalf("--yes --json: out=%q err=%v calls=%d", out, err, len(*calls))
	}
}

func TestBacklogPurgeFlagsDryRunAndConfirm(t *testing.T) {
	calls := backlogFake(t, func(c backlogCall) (int, string) {
		var req apitypes.BacklogPurgeRequest
		_ = json.Unmarshal([]byte(c.body), &req)
		if req.DryRun {
			return 200, `{"deleted":["B1","B2"],"skipped":["B9"]}`
		}
		return 200, `{"deleted":["B1","B2"],"skipped":["B9"]}`
	})
	if _, err := runBacklog(t, "backlog", "purge"); err == nil {
		t.Fatal("purge without --done/--dropped accepted")
	}
	if _, err := runBacklog(t, "backlog", "purge", "--done", "--older-than", "soon"); err == nil {
		t.Fatal("bad --older-than accepted")
	}
	if len(*calls) != 0 {
		t.Fatalf("invalid purge reached the daemon: %v", *calls)
	}

	out, err := runBacklog(t, "backlog", "purge", "--done", "--dropped", "--older-than", "30d", "--dry-run")
	if err != nil || !strings.Contains(out, "would delete 2 item(s): B1, B2") || !strings.Contains(out, "skipped B9") {
		t.Fatalf("dry run: out=%q err=%v", out, err)
	}
	var req apitypes.BacklogPurgeRequest
	c := (*calls)[0]
	if err := json.Unmarshal([]byte(c.body), &req); err != nil || c.method != "POST" || c.path != "/api/v1/projects/demo/backlog/purge" ||
		!req.DryRun || strings.Join(req.States, ",") != "done,dropped" || req.OlderThanDays == nil || *req.OlderThanDays != 30 {
		t.Fatalf("dry-run request = %+v body=%s", c, c.body)
	}

	// Without --yes: a dry-run preview, the prompt, then the real call only on yes.
	*calls = nil
	out, err = runBacklogWithStdin(t, "n\n", "backlog", "purge", "--done")
	if err != nil || !strings.Contains(out, "aborted") || len(*calls) != 1 {
		t.Fatalf("declined purge: out=%q err=%v calls=%d", out, err, len(*calls))
	}
	*calls = nil
	out, err = runBacklogWithStdin(t, "yes\n", "backlog", "purge", "--done")
	if err != nil || !strings.Contains(out, "deleted 2 item(s)") || len(*calls) != 2 {
		t.Fatalf("confirmed purge: out=%q err=%v calls=%d", out, err, len(*calls))
	}
	if last := (*calls)[1]; strings.Contains(last.body, "dryRun") {
		t.Fatalf("real purge still dry-run: %s", last.body)
	}
	// --yes: one real call, no preview.
	*calls = nil
	if _, err := runBacklog(t, "backlog", "purge", "--dropped", "--yes"); err != nil || len(*calls) != 1 || strings.Contains((*calls)[0].body, "dryRun") {
		t.Fatalf("--yes purge: err=%v calls=%v", err, *calls)
	}
}
