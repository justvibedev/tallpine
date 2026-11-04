package chat

import (
	"runtime"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// fenceGOOS is the host OS chat launches are admitted for; tests swap it.
var fenceGOOS = runtime.GOOS

// admitChatFence refuses a chat launch the hardened profile requires to be
// fenced when the harness's chat driver cannot fence it on this OS. Every chat
// launch (start, resume, branch, side chat, background and suggestion tasks)
// runs it at driver resolution with the decision it launches under and the
// permission mode it launches in; the mode is capped here as the driver caps
// it, since a driver can fence only some modes (Codex: no inner sandbox).
func admitChatFence(driver any, harness domain.AgentHarness, decision agentpolicy.Decision, permissions ports.PermissionMode) error {
	launched := permissioncap.Of(decision).LowerLive(harness, permissions)
	return protectstate.Of(decision).Admit(ports.FencesLaunch(driver, fenceGOOS, launched), string(harness), fenceGOOS)
}
