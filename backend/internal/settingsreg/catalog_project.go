package settingsreg

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// projectSettings declares the fields of a project's config. The user writes
// any of them (PUT /projects/{id}/config). The orchestrator of the project that
// registered this one writes a subset through the same route
// (ActorCreatorOrchestrator; the httpd agentConfigKeys allowlist, pinned by a
// test there). The project's own orchestrator writes any of them through PATCH
// /settings/values, and agentRules, orchestratorRules and the compaction
// fields through PUT /projects/{id}/rules (B189).
func projectSettings() []Setting {
	// The project's own orchestrator may change any of its project's settings
	// (B202 Q1); managed config and env locks still apply.
	user := []Actor{ActorUser, ActorOrchestrator}
	creator := []Actor{ActorUser, ActorOrchestrator, ActorCreatorOrchestrator}
	rules := []Actor{ActorUser, ActorOrchestrator, ActorCreatorOrchestrator}
	byCreator := func(s Setting) Setting { s.Writers = creator; return s }
	p := func(name string, kind Kind, def any, desc string) Setting {
		return Setting{Key: Key("project." + name), Scope: ScopeProject, Kind: kind, Default: jsonOf(def), Description: desc, Writers: user,
			Nullable: true, Validate: projectFieldValidator(name)}
	}
	agentRules := p("agentRules", KindText, "", "Standing instructions for worker sessions.")
	agentRules.Writers = rules
	agentRules.MaxBytes = domain.MaxAgentRulesBytes
	orchestratorRules := p("orchestratorRules", KindText, "", "Standing instructions for the project's orchestrator.")
	orchestratorRules.Writers = rules
	orchestratorRules.MaxBytes = domain.MaxAgentRulesBytes
	// Read by domain.DefaultAutoCompactChoice; the project's orchestrator
	// writes it through the rules route (B193).
	compactWindow := p("orchestrator.autoCompactWindow", KindString, domain.OrchestratorAutoCompactDefault,
		"Context size at which the project's orchestrators compact: a size from 50k to 1m (such as 120k), a percent of the model window, or off.")
	compactWindow.Writers = rules
	env := p("env", KindStringMap, nil, "Environment variables set in every session.")
	env.Sensitive = true
	// Read by the idle compaction sweep (B204); same writers.
	idleCompact := p("orchestrator.idleCompactAt", KindString, domain.OrchestratorIdleCompactDefault,
		"Context size above which an idle orchestrator compacts between tasks: a percent of its compaction window (not the model window), a size such as 36k, or off.")
	idleCompact.Writers = rules
	idleAction := p("orchestrator.idleAction", KindEnum, "",
		"What an idle orchestrator past its threshold does: compact (empty, the default) or handoff to a fresh orchestrator that rebuilds its state from the daemon.")
	idleAction.Enum = []string{"", domain.IdleActionCompact, domain.IdleActionHandoff}
	// Read by orchestrator research mode (B413); same writers.
	researchWindow := p("orchestrator.researchCompactWindow", KindString, domain.OrchestratorResearchCompactDefault,
		"Compaction window of an orchestrator in research mode: a size from 50k to 1m (such as 250k), a percent of the model window, or off to disable research mode for the project.")
	researchWindow.Writers = rules
	mode := p("defaultSessionMode", KindEnum, "", "Interface new sessions of the project open in; empty follows the app setting.")
	mode.Enum = []string{"", "chat", "tui"}
	planning := p("planningMode", KindEnum, "", "When the project plans before it builds: auto (the orchestrator decides), always or off. Empty follows the app default (Settings > Reviews); an explicit auto stays auto. A managed policy.planner.mode wins.")
	planning.Enum = []string{"", "auto", "always", "off"}
	return []Setting{
		p("canonicalRepoURL", KindString, "", "Canonical repository URL of the project."),
		p("githubUser", KindString, "", "GitHub account the project's sessions act as."),
		byCreator(p("defaultBranch", KindString, "", "Branch worktrees are created from.")),
		byCreator(p("sessionPrefix", KindString, "", "Prefix of session IDs and branches.")),
		env,
		p("symlinks", KindStringList, nil, "Paths symlinked from the main checkout into each worktree."),
		p("postCreate", KindStringList, nil, "Commands run after a worktree is created."),
		p("cloneWorktreeInclude", KindBool, false, "Copy .worktreeinclude files into new worktrees."),
		p("preRemove", KindStringList, nil, "Commands run before a worktree is removed."),
		agentRules,
		p("agentRulesFile", KindString, "", "Repo-relative file appended to the worker rules."),
		p("verifyCommand", KindString, "", "Command that verifies a worker's change."),
		orchestratorRules,
		mode,
		p("agentConfig", KindObject, nil, "Default harness, model and permissions of the project's sessions."),
		lockable(byCreator(p("worker", KindObject, nil, "Worker overrides of agentConfig."))),
		lockable(byCreator(p("orchestrator", KindObject, nil, "Orchestrator overrides of agentConfig."))),
		planning,
		lockable(byCreator(p("planner", KindObject, nil, "Planner overrides of agentConfig; empty inherits the orchestrator's agent, else the worker's. A managed policy.planner wins."))),
		compactWindow,
		idleCompact,
		researchWindow,
		idleAction,
		lockable(byCreator(p("reviewers", KindObject, nil, "Native reviewers of the project's PRs."))),
		lockable(p("reviewRisk", KindObject, nil, "Paths that need a stronger review, and the stronger reviewer (agent, model, effort) they get.")),
		lockable(p("trackerIntake", KindObject, nil, "Issue tracker intake.")),
		p("containerReap", KindObject, nil, "Reaping of session containers."),
		lockable(p("autoReview", KindBool, false, "Start a native review when a worker opens a PR.")),
		p("autoReviewMaxRounds", KindInt, 0, "Stop automatic re-reviews of a PR after this many reviews that requested changes, and tell the orchestrator (0 = never stop)."),
		p("approveIdenticalPatch", KindBool, false, "Keep a review approval when a push leaves the patch identical (a clean rebase) instead of reviewing again."),
		lockable(p("terminateOnPrMerge", KindBool, true, "End each new worker once its pull requests are merged or closed (at least one merged), none is open, it is idle and no message waits for it; the orchestrator gets an [auto] notice. On unless turned off.")),
		p("autoReviewOnReport", KindBool, true, "Start the native review as soon as a worker reports a PR ready, without waiting for it to go idle."),
		p("skillsOfferOff", KindBool, false, "Do not offer repository skills to sessions."),
		p("skillsDeselected", KindStringList, nil, "Repository skills not offered to sessions."),
		p("reportDigestCount", KindInt, 0, "Deliver queued non-decision worker reports together once this many wait (0 = wait out the time delay)."),
		creatorOnly(p("planInPlace", KindBool, false, "Let `tallpine spawn --backlog` start an implementer that writes its own plan (questions, plan, then waits read-only for approval) when the item needs one, instead of a separate planner. Off by default; the user, or the orchestrator of the project that registered this one, may turn it on (never a project's own orchestrator); a managed policy.planner.inPlace of off locks it off.")),
		creatorOnly(p("eval", KindBool, false, "Marks a throwaway evaluation project. The orchestrator that registered it may drive its backlog, plans and sessions from its own project and answer and approve plans as the owner's proxy (B335). The user or the creating orchestrator may set it, never a project's own orchestrator or a worker.")),
		humanOnly(p("planAutoApprove", KindBool, false, "Approve submitted plans automatically, without the owner. For unattended throwaway test or benchmark projects only; only a person can set it. Unset follows the app default (Settings > Reviews); an explicit false stays off. A managed policy.planner.autoApprove of off locks it off.")),
		byCreator(p("noCI", KindBool, false, "The project has no CI; workers do not wait for it.")),
		lockable(p("models", KindObject, nil, "Models allowed in the project.")),
		p("promptCacheTTL", KindObject, nil, "Prompt cache TTL per session role."),
		p("chatHibernation", KindObject, nil, "Project override of Chat hibernation."),
	}
}

// humanOnly restricts a project field to the user: no agent, not even the
// project's own orchestrator, may write it.
func humanOnly(s Setting) Setting { s.Writers = []Actor{ActorUser}; return s }

// creatorOnly marks a field the user and the creating orchestrator may write,
// but not a project's own orchestrator: an orchestrator may enable it only on
// projects it registered itself (benchmark and eval projects).
func creatorOnly(s Setting) Setting {
	s.Writers = []Actor{ActorUser, ActorCreatorOrchestrator}
	return s
}

// lockable marks a project field the managed file may set and lock.
func lockable(s Setting) Setting { s.MDMLockable = true; return s }

// projectFieldValidator validates one project config field by placing it in an
// otherwise empty config and running the config's own validator. A dotted name
// ("orchestrator.autoCompactWindow") is nested. Decoding is strict, as on the
// config routes, so a misspelled nested field is refused. Checks that depend
// on the stored project (the agent-rules shrink allowance, scratch projects,
// the canonical repository) stay on the owner's write path.
func projectFieldValidator(name string) func(json.RawMessage) error {
	parts := strings.Split(name, ".")
	return func(raw json.RawMessage) error {
		doc := raw
		for i := len(parts) - 1; i >= 0; i-- {
			var err error
			if doc, err = json.Marshal(map[string]json.RawMessage{parts[i]: doc}); err != nil {
				return err
			}
		}
		dec := json.NewDecoder(bytes.NewReader(doc))
		dec.DisallowUnknownFields()
		var cfg domain.ProjectConfig
		if err := dec.Decode(&cfg); err != nil {
			return err
		}
		if dec.More() {
			return errors.New("has trailing data")
		}
		return cfg.Validate()
	}
}
