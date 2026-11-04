package migratevo

import (
	"context"
	"fmt"
	"os"
)

// Options select what Run does.
type Options struct {
	DryRun             bool
	CopyHarnessHistory bool
}

func (e *Env) printf(format string, a ...any) {
	if e.Out != nil {
		_, _ = fmt.Fprintf(e.Out, format, a...)
	}
}

// Describe prints the plan: every rename, DB update count, worktree repair and
// Claude dir copy.
func (e *Env) Describe(p *Plan) {
	e.printf("rename %s -> %s (the old root is removed)\n", p.Source, p.Target)
	for _, u := range p.DBUpdates {
		e.printf("db: %s.%s: %d rows\n", u.Table, u.Column, u.Rows)
	}
	e.printf("git worktree repair: %d worktrees\n", len(p.Worktrees))
	for _, w := range p.Worktrees {
		e.printf("  repo %s: %s -> %s\n", w.Repo, w.Old, w.New)
	}
	for _, a := range p.Accounts {
		e.printf("account %s (%s): home %s moves; sign in again after migration (credentials are not migrated)\n", a.ID, a.Provider, a.Home)
	}
	if !p.CopyHarnessHistory {
		e.printf("claude dirs: disabled (--copy-harness-history=false)\n")
	}
	for _, c := range p.ClaudeCopies {
		if c.Skip != "" {
			e.printf("claude: skip session %s: %s\n", c.Session, c.Skip)
			continue
		}
		e.printf("claude: copy %s -> %s (%d bytes)\n", c.Src, c.Dst, c.Bytes)
	}
}

// Migrate executes the migration (or, with DryRun, only describes it). Steps
// are idempotent and recorded in the plan file, so a failed run resumes by
// calling Migrate again.
func (e *Env) Migrate(ctx context.Context, opts Options) error {
	plan, err := e.LoadPlan()
	if err != nil {
		return err
	}
	if opts.DryRun {
		if plan != nil {
			e.printf("resuming recorded plan %s\n", e.FindPlanFile())
			e.Describe(plan)
			return nil
		}
		if err := e.CheckPreconditions(ctx, false); err != nil {
			return err
		}
		p, err := e.ComputePlan(ctx, opts.CopyHarnessHistory)
		if err != nil {
			return err
		}
		e.Describe(p)
		return nil
	}
	if err := e.CheckPreconditions(ctx, plan != nil); err != nil {
		return err
	}
	if plan == nil {
		if _, err := e.SetAsidePreexistingTarget(); err != nil {
			return err
		}
		if plan, err = e.ComputePlan(ctx, opts.CopyHarnessHistory); err != nil {
			return err
		}
		if err := e.SavePlan(plan); err != nil {
			return err
		}
	}
	e.Describe(plan)
	return e.runSteps(ctx, plan)
}

type step struct {
	name string
	do   func(ctx context.Context, p *Plan) error
}

func (e *Env) runSteps(ctx context.Context, p *Plan) error {
	steps := []step{
		{StepRename, func(context.Context, *Plan) error { return e.RenameHome() }},
		{StepDB, func(ctx context.Context, p *Plan) error {
			if _, err := os.Stat(e.TargetDB()); err != nil {
				return fmt.Errorf("target db: %w", err)
			}
			updated, err := RewriteDB(ctx, e.TargetDB(), p.Source, p.Target)
			for _, u := range updated {
				e.printf("db: %s.%s: rewrote %d rows\n", u.Table, u.Column, u.Rows)
			}
			return err
		}},
		{StepWorktrees, func(ctx context.Context, p *Plan) error {
			n, failures := e.RepairWorktrees(ctx, p.Worktrees)
			e.printf("worktrees repaired and verified: %d\n", n)
			if len(failures) > 0 {
				return fmt.Errorf("%d worktree failures, first: %s", len(failures), failures[0])
			}
			return nil
		}},
		{StepClaude, func(ctx context.Context, p *Plan) error {
			if !p.CopyHarnessHistory {
				return nil
			}
			n, err := e.CopyClaudeDirs(ctx, p.ClaudeCopies)
			e.printf("claude dirs copied: %d\n", n)
			return err
		}},
		{StepLaunchd, hookStep(e.LaunchdHook)}, // step 6: wired by the cut-over PR
		{StepCLI, func(context.Context, *Plan) error { return e.InstallCLILinks() }},
		{StepDaemon, hookStep(e.DaemonHook)}, // step 8: wired by the cut-over PR
	}
	for _, s := range steps {
		if p.Done(s.name) {
			continue
		}
		if err := s.do(ctx, p); err != nil {
			return fmt.Errorf("step %s: %w", s.name, err)
		}
		if (s.name == StepLaunchd && e.LaunchdHook == nil) || (s.name == StepDaemon && e.DaemonHook == nil) {
			e.printf("step %s: not wired yet, skipped\n", s.name)
			continue
		}
		p.Steps[s.name] = true
		if err := e.SavePlan(p); err != nil {
			return err
		}
	}
	return nil
}

func hookStep(h Hook) func(context.Context, *Plan) error {
	return func(ctx context.Context, p *Plan) error {
		if h == nil {
			return nil
		}
		return h(ctx, p)
	}
}
