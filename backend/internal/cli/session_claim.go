package cli

import (
	"context"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func (c *commandContext) claimSessionPR(ctx context.Context, cmd *cobra.Command, id, ref string, opts sessionClaimPROptions) error {
	sess, err := c.fetchScopedSession(ctx, id, opts.project)
	if err != nil {
		return err
	}
	project, err := c.fetchProjectDetails(ctx, string(sess.ProjectID))
	if err != nil {
		return err
	}
	resolvedRef, err := c.resolvePRRef(ctx, ref, project)
	if err != nil {
		return err
	}
	var res apitypes.ClaimPRResponse
	req := apitypes.ClaimPRRequest{PR: resolvedRef, AllowTakeover: ptrBool(!opts.noTakeover)}
	if err := c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/pr/claim", req, &res); err != nil {
		return err
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	return writeClaimPRResult(cmd, res)
}

func (c *commandContext) fetchProjectDetails(ctx context.Context, id string) (projectDetails, error) {
	var res projectGetResult
	if err := c.getJSON(ctx, "projects/"+url.PathEscape(id), &res); err != nil {
		return projectDetails{}, err
	}
	return res.Project, nil
}

func writeClaimPRResult(cmd *cobra.Command, res apitypes.ClaimPRResponse) error {
	out := cmd.OutOrStdout()
	if len(res.PRs) == 0 {
		if _, err := fmt.Fprintf(out, "session %s claimed PR\n", res.SessionID); err != nil {
			return err
		}
	} else {
		pr := res.PRs[0]
		if _, err := fmt.Fprintf(out, "session %s claimed PR #%d\n", res.SessionID, pr.Number); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "  pr:       %s\n", pr.URL); err != nil {
			return err
		}
	}
	if err := writeClaimPRCheckout(out, res.BranchChanged); err != nil {
		return err
	}
	for _, owner := range res.TakenOverFrom {
		if _, err := fmt.Fprintf(out, "  taking over from %s\n", owner); err != nil {
			return err
		}
	}
	return nil
}
