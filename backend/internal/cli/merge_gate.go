package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
	"tallpine.eu/tallpine/backend/internal/process"
	mergessvc "tallpine.eu/tallpine/backend/internal/service/merges"
)

func newMergeGateCommand(ctx *commandContext) *cobra.Command {
	var project string
	var force, noMerge bool
	cmd := &cobra.Command{
		Use:   "merge <pr-number>",
		Short: "Run the orchestrator merge gate for a PR and squash-merge it",
		Long: "Runs the whole merge gate in one command from the repository checkout: refuses when another older in-flight PR " +
			"overlaps this one (the `tallpine merges` check; --force skips it) or when the PR head has no Tallpine-approved review " +
			"or GitHub approval (an identical-patch rebase keeps the approval); then builds the merge with origin/main in a temp " +
			"worktree, runs `scripts/verify --changed` there, re-checks the head and main and " +
			"squash-merges with [skip ci] pinned to the gated head. Prints one line: MERGED <sha>, or FAILED <step> log=<path>. " +
			"Use `tallpine pr merge` to merge through the daemon without the gate.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			prNumber, err := normalizePRNumber(args[0])
			if err != nil {
				return err
			}
			if !force {
				if msg, err := ctx.mergeOverlapRefusal(cmd.Context(), project, prNumber); err != nil {
					return err
				} else if msg != "" {
					return fmt.Errorf("FAILED overlap: %s (use --force to override)", msg)
				}
			}
			// A missing Tallpine approval is not an error here: a GitHub approval may still apply.
			var req apitypes.MergePRRequest
			if err := ctx.resolveMergeTarget(cmd.Context(), prNumber, &req); err != nil {
				req.ExpectedHeadSHA = ""
			}
			top, err := process.CommandContext(cmd.Context(), "git", "rev-parse", "--show-toplevel").Output()
			if err != nil {
				return errors.New("run `tallpine merge` from the repository checkout")
			}
			return runMergeGate(cmd, strings.TrimSpace(string(top)), prNumber, req.ExpectedHeadSHA, noMerge)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "merge even if an older in-flight PR touches the same files")
	cmd.Flags().BoolVar(&noMerge, "no-merge", false, "run every gate step but stop before the merge (prints READY <sha>)")
	addBacklogProjectFlag(cmd, &project, new(bool))
	cmd.Flags().Lookup("json").Hidden = true
	return cmd
}

func runMergeGate(cmd *cobra.Command, top, prNumber, approvedSHA string, noMerge bool) error {
	args := []string{prNumber}
	if approvedSHA != "" {
		args = append(args, "--tallpine-approved", approvedSHA)
	}
	if noMerge {
		args = append(args, "--no-merge")
	}
	gate := process.CommandContext(cmd.Context(), top+"/scripts/merge-gate", args...)
	gate.Dir = top
	out, err := gate.Output()
	line := strings.TrimSpace(string(out))
	if line != "" {
		if _, werr := fmt.Fprintln(cmd.OutOrStdout(), line); werr != nil {
			return werr
		}
	}
	if err != nil {
		if line == "" {
			var exit *exec.ExitError
			if errors.As(err, &exit) && strings.TrimSpace(string(exit.Stderr)) != "" {
				return fmt.Errorf("merge gate failed: %s", strings.TrimSpace(string(exit.Stderr)))
			}
			return fmt.Errorf("merge gate failed: %w", err)
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exitCodeError{code: exit.ExitCode()}
		}
		return err
	}
	return nil
}

// mergeOverlapRefusal returns a reason when an in-flight PR that should merge first overlaps prNumber.
func (c *commandContext) mergeOverlapRefusal(ctx context.Context, project, prNumber string) (string, error) {
	resolved, err := c.resolveSpawnProject(ctx, project)
	if err != nil {
		return "", err
	}
	var d mergessvc.Digest
	if err := c.getJSON(ctx, "projects/"+url.PathEscape(resolved.ID)+"/merges", &d); err != nil {
		return "", err
	}
	n, _ := strconv.Atoi(prNumber)
	return overlapRefusal(d, n), nil
}

func overlapRefusal(d mergessvc.Digest, pr int) string {
	for _, o := range d.Overlaps {
		if o.Number == pr && !o.WithMerged {
			return o.Advice
		}
	}
	return ""
}
