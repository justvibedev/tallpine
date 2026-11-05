package kiro

import (
	"context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var (
	_ ports.AgentInterfaceHandoff             = (*Plugin)(nil)
	_ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
)

// kiroSessionPrefix is the prefix Kiro v3 gives every session id it issues and
// every session directory under ~/.kiro/sessions/<cwd-hash>/.
const kiroSessionPrefix = "sess_"

// NativeConversationID bridges Kiro's terminal resume id and its ACP session
// id. Kiro v3 keeps one session store for `kiro-cli chat` and `kiro-cli acp`:
// the id ACP session/new returns resumes with `kiro-cli chat --v3 --resume-id`,
// and a terminal session loads over ACP session/load (verified live on
// kiro-cli 3.0.0). A terminal source must have reported its id through the
// hooks before it can switch without losing context.
//
// The id is always returned in its canonical `sess_` form: kiro-cli 3.0.0
// answers session/load for an unprefixed id by silently creating a new, empty
// session under that name, which would drop the conversation.
func (p *Plugin) NativeConversationID(
	ctx context.Context,
	session ports.SessionRef,
	currentMode domain.SessionMode,
	providerConversationID string,
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	id := session.Metadata[ports.MetadataKeyAgentSessionID]
	if currentMode == domain.SessionModeChat {
		id = providerConversationID
	}
	id = canonicalKiroSessionID(id)
	return id, id != "", nil
}

// NativeConversationExists answers false only on positive proof that the
// conversation is empty: ACP session/new writes session.json, and
// messages.jsonl appears with the first prompt, so a v3 session directory
// holding session.json but no messages.jsonl carries no context and the target
// interface may start fresh. Every other case reads as present. The probe also
// gates every terminal restore and idle update, and Kiro resumes ids this
// store may not hold (v3 reads v2-created sessions), so an id it cannot find
// must keep resuming natively rather than relaunch fresh.
func (p *Plugin) NativeConversationExists(
	ctx context.Context,
	_ ports.SessionRef,
	nativeConversationID string,
	_ map[string]string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id := canonicalKiroSessionID(nativeConversationID)
	if id == "" {
		return false, nil
	}
	root, err := p.kiroSessionsRoot()
	if err != nil {
		return false, err
	}
	if findSessionDir(root, id) != "" {
		return true, nil
	}
	return findSessionDirWith(root, id, "session.json") == "", nil
}

func canonicalKiroSessionID(id string) string {
	bare := strings.TrimPrefix(strings.TrimSpace(id), kiroSessionPrefix)
	if bare == "" || !sessionIDPattern.MatchString(bare) {
		return ""
	}
	return kiroSessionPrefix + bare
}
