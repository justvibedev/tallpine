package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/gitdefault"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/agentaccounts"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/service/contextbudget"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// contextHealthTick paces the context health check (B825). Each project is
// measured at most once a day (the run is persisted); a short tick only picks
// up a newly enabled project, or a queued card whose slot freed, within the hour.
const contextHealthTick = time.Hour

// promptRenderer is the part of the session manager that renders a system prompt.
type promptRenderer interface {
	TallpinePrompt(ctx context.Context, project domain.ProjectID, kind domain.SessionKind, h domain.AgentHarness) (string, error)
}

var _ promptRenderer = (*sessionmanager.Manager)(nil)

// startContextHealth measures opted-in projects daily and files findings as
// owner cards. It never edits a repository and never messages an orchestrator.
func startContextHealth(ctx context.Context, cfg config.Config, store *sqlite.Store, asks contextbudget.Asks, sessMgr any, accounts *agentaccounts.Service, log *slog.Logger) {
	resolver := gitdefault.New("", nil)
	deps := contextbudget.Deps{
		Store: store, Asks: asks, Log: log,
		ArtifactRoot: filepath.Join(cfg.DataDir, "artifacts"),
		Reader: func(ctx context.Context, proj domain.ProjectRecord) (contextbudget.Source, error) {
			ref := "HEAD"
			if res, err := resolver.Inspect(ctx, proj.Path); err == nil && res.Ref != "" {
				ref = res.Ref
			}
			return contextbudget.GitSource(proj.Path, ref), nil
		},
		Memory: func(ctx context.Context, proj domain.ProjectRecord) (*contextbudget.Memory, error) {
			return claudeMemory(ctx, store, proj)
		},
	}
	if pr, ok := sessMgr.(promptRenderer); ok {
		deps.Prompt = func(ctx context.Context, proj domain.ProjectRecord, kind domain.SessionKind, h domain.AgentHarness) (string, error) {
			return pr.TallpinePrompt(ctx, domain.ProjectID(proj.ID), kind, h)
		}
	}
	deps.Finders = []contextbudget.Finder{
		contextbudget.BudgetFinder(),
		contextbudget.RoutingFinder{Store: store, Reader: deps.Reader, ArtifactRoot: deps.ArtifactRoot},
	}
	if sessions, ok := sessMgr.(planSessions); ok && accounts != nil {
		deps.Analyst = newContextAnalyst(deps.ArtifactRoot, sessions, accounts, log)
	}
	sweeper := contextbudget.New(deps)
	go func() {
		ticker := time.NewTicker(contextHealthTick)
		defer ticker.Stop()
		for {
			if err := sweeper.Sweep(ctx); err != nil && ctx.Err() == nil {
				log.Warn("context health check failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// claudeMemory finds the project's Claude orchestrator memory: the memory
// directory of the project path or of an orchestrator's workspace, whichever
// has the largest index. Nil when there is none.
func claudeMemory(ctx context.Context, store *sqlite.Store, proj domain.ProjectRecord) (*contextbudget.Memory, error) {
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	if home == "" {
		u, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(u, ".claude")
	}
	cwds := []string{proj.Path}
	recs, err := store.ListSessions(ctx, domain.ProjectID(proj.ID))
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		if r.Kind == domain.KindOrchestrator && r.Harness == domain.HarnessClaudeCode && r.Metadata.WorkspacePath != "" {
			cwds = append(cwds, r.Metadata.WorkspacePath)
		}
	}
	var best *contextbudget.Memory
	for _, cwd := range cwds {
		m, ok, err := contextbudget.MeasureMemory(contextbudget.ClaudeMemoryDir(home, cwd))
		if err != nil || !ok {
			continue
		}
		if best == nil || m.IndexTokens > best.IndexTokens {
			m := m
			best = &m
		}
	}
	return best, nil
}

// contextHealthWork files the backlog item and starts the worker a Yes asks for.
type contextHealthWork struct {
	backlog  *backlogsvc.Service
	sessions planSessions
}

func newContextHealthHandler(store *sqlite.Store, backlog *backlogsvc.Service, sessions planSessions, log *slog.Logger) *contextbudget.Handler {
	return &contextbudget.Handler{Store: store, Work: contextHealthWork{backlog: backlog, sessions: sessions}, Log: log}
}

func (w contextHealthWork) FileItem(ctx context.Context, project domain.ProjectID, in contextbudget.ItemInput) (contextbudget.Item, error) {
	v, err := w.backlog.Create(ctx, project, backlogsvc.Actor{Kind: domain.BacklogOriginHuman}, backlogsvc.CreateInput{
		Title: in.Title, Note: in.Note, DoneWhen: in.DoneWhen, Open: true,
	})
	if err != nil {
		return contextbudget.Item{}, err
	}
	return contextbudget.Item{ID: v.ID, Ref: v.Ref()}, nil
}

func (w contextHealthWork) StartWorker(ctx context.Context, project domain.ProjectID, item contextbudget.Item, brief string) (domain.SessionID, error) {
	prep, err := w.backlog.PrepareSpawn(ctx, project, item.Ref, false)
	if err != nil {
		return "", err
	}
	sess, err := spawnInheritingHarness(ctx, w.sessions, ports.SpawnConfig{
		ProjectID: project, Kind: domain.KindWorker, LaneItemID: prep.ParentID,
		Prompt: "Work on backlog item " + item.Ref + ".", BacklogContext: prep.Context + "\n\n" + brief,
		DisplayName: "Context health: " + item.Ref,
	})
	if err != nil {
		return "", err
	}
	if err := w.backlog.LinkSession(ctx, prep.ID, sess.ID, "human"); err != nil {
		slog.Warn("could not link the context health worker to its item", "item", item.Ref, "session", sess.ID, "error", err)
	}
	return sess.ID, nil
}

// newContextAnalyst wires the weekly analyst: a claude-code, Haiku, low-effort
// worker the daemon starts when the default Claude account has room.
func newContextAnalyst(root string, sessions planSessions, accounts *agentaccounts.Service, log *slog.Logger) *contextbudget.Analyst {
	return &contextbudget.Analyst{
		Root: root, Log: log,
		Limits: func(ctx context.Context) (domain.AgentAccountLimits, error) {
			views, err := accounts.List(ctx, false)
			if err != nil {
				return domain.AgentAccountLimits{}, err
			}
			for _, v := range views {
				if v.Account.Provider == domain.AgentAccountProviderClaude && v.Default && v.Limits != nil && v.LimitsErr == "" {
					return *v.Limits, nil
				}
			}
			return domain.AgentAccountLimits{}, errors.New("no readable limits for the default Claude account")
		},
		Spawn: func(ctx context.Context, project domain.ProjectID, prompt string) (domain.SessionID, error) {
			sess, _, _, err := sessions.Spawn(ctx, ports.SpawnConfig{
				ProjectID: project, Kind: domain.KindWorker, Role: domain.SessionRoleContextAnalyst,
				Harness: domain.HarnessClaudeCode, AgentConfig: ports.AgentConfig{Model: "haiku", Effort: "low"},
				Prompt: prompt, DisplayName: "Context analyst",
			})
			return sess.ID, err
		},
		Kill: func(ctx context.Context, id domain.SessionID) error {
			_, err := sessions.Kill(ctx, id)
			return err
		},
		Ended: func(ctx context.Context, id domain.SessionID) bool {
			sess, err := sessions.Get(ctx, id)
			return err != nil || sess.IsTerminated
		},
	}
}
