package cli

import (
	"errors"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
)

type sessionOptions struct {
	project string
	json    bool
}

type sessionListOptions struct {
	sessionOptions
	all               bool
	includeTerminated bool
}

type sessionCleanupOptions struct {
	project string
	yes     bool
	dryRun  bool
}

type sessionClaimPROptions struct {
	project    string
	json       bool
	noTakeover bool
}

func newSessionCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage agent sessions",
	}
	cmd.AddCommand(newSessionListCommand(ctx))
	cmd.AddCommand(newSessionTopCommand(ctx))
	cmd.AddCommand(newSessionGetCommand(ctx))
	cmd.AddCommand(newSessionKillCommand(ctx))
	cmd.AddCommand(newSessionRestoreCommand(ctx))
	cmd.AddCommand(newSessionExitAgentCommand(ctx))
	cmd.AddCommand(newSessionResumeAgentCommand(ctx))
	cmd.AddCommand(newSessionRenameCommand(ctx))
	cmd.AddCommand(newSessionCleanupCommand(ctx))
	cmd.AddCommand(newSessionClaimPRCommand(ctx))
	cmd.AddCommand(newSessionLandCommand(ctx))
	cmd.AddCommand(newSessionHistoryCommand(ctx))
	cmd.AddCommand(newSessionUsageCommand(ctx))
	cmd.AddCommand(newSessionTaskCommand(ctx))
	cmd.AddCommand(newSessionApprovalsCommand(ctx), newSessionResolveApprovalCommand(ctx, "approve", "allow_once"), newSessionResolveApprovalCommand(ctx, "deny", "reject_once"), newSessionAnswerCommand(ctx))
	return cmd
}

func newSessionListCommand(ctx *commandContext) *cobra.Command {
	var opts sessionListOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List sessions",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.listSessions(cmd.Context(), cmd, opts)
		},
	}
	f := cmd.Flags()
	addSessionProjectFlag(f, &opts.project, "Filter by project ID")
	f.BoolVarP(&opts.all, "all", "a", false, "Include orchestrator sessions")
	f.BoolVar(&opts.includeTerminated, "include-terminated", false, "Include terminated sessions")
	f.BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func newSessionGetCommand(ctx *commandContext) *cobra.Command {
	var opts sessionOptions
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Fetch one session",
		Args:  oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.getSession(cmd.Context(), cmd, id, opts)
		},
	}
	f := cmd.Flags()
	addSessionProjectFlag(f, &opts.project, "Project id to scope the lookup")
	f.BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func newSessionKillCommand(ctx *commandContext) *cobra.Command {
	var opts sessionOptions
	cmd := &cobra.Command{
		Use:   "kill <id>",
		Short: "Terminate a session",
		Args:  oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.killSession(cmd.Context(), cmd, id, opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to scope the lookup")
	return cmd
}

func newSessionRestoreCommand(ctx *commandContext) *cobra.Command {
	var opts sessionOptions
	cmd := &cobra.Command{
		Use:   "restore <id>",
		Short: "Restore a terminated session or resume an exited agent",
		Args:  oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.restoreSession(cmd.Context(), cmd, id, opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to scope the lookup")
	return cmd
}

func newSessionExitAgentCommand(ctx *commandContext) *cobra.Command {
	var opts sessionOptions
	cmd := &cobra.Command{
		Use:   "exit-agent <id>",
		Short: "Exit an agent without terminating its Tallpine session",
		Args:  oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.exitSessionAgent(cmd.Context(), cmd, id, opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to scope the lookup")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func newSessionResumeAgentCommand(ctx *commandContext) *cobra.Command {
	var opts sessionOptions
	cmd := &cobra.Command{
		Use:   "resume-agent <id>",
		Short: "Resume an exited agent in its existing Tallpine session",
		Args:  oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.resumeSessionAgent(cmd.Context(), cmd, id, opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to scope the lookup")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func newSessionRenameCommand(ctx *commandContext) *cobra.Command {
	var opts sessionOptions
	cmd := &cobra.Command{
		Use:   "rename <id> <name>",
		Short: "Rename a session",
		Args:  sessionRenameArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.renameSession(cmd.Context(), cmd, id, args[1], opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to scope the lookup")
	return cmd
}

func newSessionCleanupCommand(ctx *commandContext) *cobra.Command {
	var opts sessionCleanupOptions
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Clean up terminated sessions",
		Long:  "Clean up terminated sessions by reclaiming eligible workspaces. Dirty worktrees are skipped by the daemon.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.cleanupSessions(cmd.Context(), cmd, opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Filter by project ID")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Preview which sessions would be cleaned without removing anything")
	return cmd
}

func newSessionClaimPRCommand(ctx *commandContext) *cobra.Command {
	var opts sessionClaimPROptions
	cmd := &cobra.Command{
		Use:   "claim-pr [<session-id>] <pr-ref>",
		Short: "Attach an existing PR to a session",
		Long: "Attach an existing PR to a session. When session-id is omitted, the current session is read from TALLPINE_SESSION_ID.\n\n" +
			"Claiming updates PR ownership metadata only; it does not check out the PR branch. Verify the workspace branch and HEAD before editing.",
		Example: `  # From inside a Tallpine worker session
  tallpine session claim-pr 88

  # Target a session explicitly
  tallpine session claim-pr mer-3 88`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.RangeArgs(1, 2)(cmd, args); err != nil {
				return usageError{err}
			}
			_, _, err := resolveSessionClaimPRArgs(args)
			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, ref, err := resolveSessionClaimPRArgs(args)
			if err != nil {
				return err
			}
			return ctx.claimSessionPR(cmd.Context(), cmd, id, ref, opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to scope the lookup")
	cmd.Flags().BoolVar(&opts.noTakeover, "no-takeover", false, "Refuse if another active session owns the PR")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func resolveSessionClaimPRArgs(args []string) (sessionID, ref string, err error) {
	if len(args) == 2 {
		sessionID, err = normalizeSessionID(args[0])
		return sessionID, args[1], err
	}
	sessionID = strings.TrimSpace(brand.Getenv("SESSION_ID"))
	if sessionID == "" {
		return "", "", usageError{errors.New("session id is required (pass <session-id> or set TALLPINE_SESSION_ID)")}
	}
	sessionID, err = normalizeSessionID(sessionID)
	return sessionID, args[0], err
}

func addSessionProjectFlag(flags interface {
	StringVarP(*string, string, string, string, string)
}, target *string, usage string) {
	flags.StringVarP(target, "project", "p", "", usage)
}

func oneSessionIDArg(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return usageError{err}
	}
	if _, err := normalizeSessionID(args[0]); err != nil {
		return err
	}
	return nil
}

func sessionRenameArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(2)(cmd, args); err != nil {
		return usageError{err}
	}
	if _, err := normalizeSessionID(args[0]); err != nil {
		return err
	}
	if strings.TrimSpace(args[1]) == "" {
		return usageError{errors.New("session name is required")}
	}
	return nil
}

func apiPath(path string, params url.Values) string {
	if len(params) == 0 {
		return path
	}
	return path + "?" + params.Encode()
}

func normalizeSessionID(id string) (string, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", usageError{errors.New("session id is required")}
	}
	return trimmed, nil
}

func ptrBool(v bool) *bool { return &v }
