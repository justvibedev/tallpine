package deepseekharness

// PermissionModeEnv is the environment variable dsh's shipped profiles read to
// pick the sandbox-policy plugin's mode.
const PermissionModeEnv = "DSH_PERMISSION_MODE"

// readOnlyPatch pins the sandbox-policy plugin to read-only with a literal, so
// no environment value can widen it. In read-only mode dsh's local sandbox
// (Seatbelt `(deny file-write*)` plus /dev/null, bwrap `--ro-bind / /`,
// Landlock read-only grants) rejects every file write, and a confined mode
// refuses to execute when its backend is unavailable.
const readOnlyPatch = `# Written by Tallpine. Pins the file sandbox to read-only for reviewers.
- id: sandbox-policy
  config:
    mode: read-only
`

// ReadOnlyArgs returns the dsh launch flags that make the session's file
// sandbox read-only. Apply them after HeadlessArgs so the pin wins.
func ReadOnlyArgs(dataDir string) ([]string, error) {
	path, err := writeOverlay(dataDir, "readonly.patch.yml", readOnlyPatch)
	if err != nil {
		return nil, err
	}
	// --permission-mode records a session override, which outranks the
	// deployment default and any wider mode a resumed session carries.
	return []string{"--patch", path, "--permission-mode", "read-only"}, nil
}

// fullAccessPatch pins the file sandbox to danger-full-access for a bypass
// terminal launch. dsh's workspace-write roots are fixed (the session cwd, /tmp
// and the OS temp dir) with no way to add one, so git cannot write the shared
// .git of a linked worktree (index.lock, objects, refs) and the headless profile
// has no channel to approve an escalation. Mirrors the Chat driver, which sets
// DSH_PERMISSION_MODE=danger-full-access for bypass sessions.
const fullAccessPatch = `# Written by Tallpine. Bypass worker: let git reach the worktree's shared .git.
- id: sandbox-policy
  config:
    mode: danger-full-access
# The permission preset plugin requires sandbox and approval to match a preset.
- id: approval
  config:
    policy: never
`

// FullAccessArgs returns the dsh launch flags that lift the file sandbox.
func FullAccessArgs(dataDir string) ([]string, error) {
	path, err := writeOverlay(dataDir, "fullaccess.patch.yml", fullAccessPatch)
	if err != nil {
		return nil, err
	}
	return []string{"--patch", path}, nil
}
