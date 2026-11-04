package deepseekharnessacp

import (
	"runtime"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/deepseekharness"
	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// fenceGOOS is the host OS the driver fences for; tests swap it.
var fenceGOOS = runtime.GOOS

// fenceLaunch applies the terminal adapter's one fence rule
// (deepseekharness.FencesLaunch over the capped mode) to a described dsh ACP
// launch. The final env's DSH_PERMISSION_MODE says whether dsh really runs
// unsandboxed; when it contradicts the mode under Security mode the launch is
// refused rather than reported wrongly. A launch the hardened profile requires
// fenced that cannot be is refused too. dsh's env and arguments are unchanged.
func fenceLaunch(launch acpdriver.Launch, decision agentpolicy.Decision, mode ports.PermissionMode, workspace string) (acpdriver.Launch, error) {
	capped := permissioncap.Of(decision).Lower(domain.HarnessDeepSeek, mode)
	if err := protectstate.Of(decision).Admit(deepseekharness.FencesLaunch(fenceGOOS, capped), string(domain.HarnessDeepSeek), fenceGOOS); err != nil {
		return acpdriver.Launch{}, err
	}
	argv, err := deepseekharness.Fence(append([]string{launch.Command}, launch.Args...), launch.Env, decision, capped, deepseekharness.EnvLiftsSandbox(launch.Env), workspace)
	if err != nil {
		return acpdriver.Launch{}, err
	}
	launch.Command, launch.Args = argv[0], argv[1:]
	return launch, nil
}
