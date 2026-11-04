package sessionmanager

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/githubcreds"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// applyFenceGitConfig keeps plain `git switch -c` and `git push` working in a
// fenced (Security mode) session. The fence makes the shared .git/config
// read-only, so git cannot record upstream tracking there (B497). Git reads
// config from GIT_CONFIG_PARAMETERS (not COUNT/KEY_n: harnesses that scrub names containing KEY drop those) without any file, so with
// autoSetupMerge off nothing tries to write tracking, and push.default=current
// lets a plain push work without an upstream. An existing value is kept and ours appended.
func applyFenceGitConfig(env map[string]string, decision agentpolicy.Decision, agent any, permissions ports.PermissionMode) {
	if env == nil || !protectstate.Of(decision).Enabled() || !ports.FencesLaunch(agent, fenceGOOS, permissions) {
		return
	}
	appendGitConfig(env)
}

// applyFenceGitConfigFor is applyFenceGitConfig for session id, called from
// runtimeEnvBase, the one place spawn, relaunch, restore, account move, chat and
// the idle update all build a launch env. A session not yet stored, or without a
// harness, is not fenced here. The mode is the stored one (seeded post-cap)
// lowered again by today's cap, the mode the launch runs in.
func (m *Manager) applyFenceGitConfigFor(id domain.SessionID, harness domain.AgentHarness, env map[string]string) {
	if m.store == nil || id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), envFilterDecideTimeout)
	defer cancel()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return
	}
	if harness == "" {
		harness = rec.Harness
	}
	decision := m.decide(ctx, rec.ProjectID, rec.Kind, harness, id)
	permissions := permissioncap.Of(decision).Lower(harness, rec.Metadata.Permissions)
	applyFenceGitConfig(env, decision, m.harnessAdapter(harness), permissions)
}

func appendGitConfig(env map[string]string) {
	env[githubcreds.EnvGitConfigParameters] = githubcreds.AppendGitConfig(env,
		[2]string{"branch.autoSetupMerge", "false"}, [2]string{"push.default", "current"})
}
