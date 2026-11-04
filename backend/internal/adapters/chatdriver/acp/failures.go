package acp

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// sessionFailureEvent translates the retry/failure extension advertised by
// claude-agent-acp into an ordinary durable activity. The extension is parsed at
// the ACP boundary so neither the service nor the renderer depends on its vendor
// namespace. A stable provider item id makes successive retry attempts update one
// row; settling the enclosing turn then settles this running status with it.
func (c *conversation) sessionFailureEvent(
	turnID, sourceID string,
	meta map[string]any,
) (ports.ChatEvent, bool) {
	failure := sessionFailure(meta)
	if failure == nil {
		return ports.ChatEvent{}, false
	}
	title, _ := failure["title"].(string)
	title = strings.TrimSpace(title)

	detailMap := map[string]any{"event": "provider.failure"}
	for _, key := range []string{"category", "severity"} {
		if value, ok := failure[key].(string); ok && strings.TrimSpace(value) != "" {
			detailMap[key] = strings.TrimSpace(value)
		}
	}
	if details, ok := failure["details"].(string); ok && strings.TrimSpace(details) != "" {
		detailMap["text"] = strings.TrimSpace(details)
	}
	if revision, ok := number(failure["revision"]); ok && revision >= 0 {
		detailMap["revision"] = revision
	}
	detail, _ := json.Marshal(detailMap)
	event := ports.ChatEvent{
		Kind:           ports.ChatEventActivityStarted,
		ProviderTurnID: turnID,
		ActivityKind:   domain.ActivityKindSystem,
		ActivityStatus: domain.ActivityStatusRunning,
		Summary:        title,
		Detail:         detail,
	}
	c.mu.Lock()
	if c.providerFailure != nil && c.providerFailure.ProviderTurnID == turnID {
		event.ProviderItemID = c.providerFailure.ProviderItemID
	} else {
		// One row per uninterrupted retry episode. Host identity survives replay;
		// direct connections have no replay ID and need a fresh local identity.
		if sourceID == "" {
			sourceID = uuid.NewString()
		}
		event.ProviderItemID = c.providerItemID("session-failure:" + sourceID)
	}
	c.providerFailure = &event
	c.mu.Unlock()
	return event, true
}

func sessionFailure(meta map[string]any) map[string]any {
	air := nestedMap(nestedMap(meta, "jetbrains"), "air")
	version, versionOK := number(air["version"])
	failure := nestedMap(air, "sessionFailure")
	id, _ := failure["id"].(string)
	title, _ := failure["title"].(string)
	if !versionOK || version < 1 || strings.TrimSpace(id) == "" || strings.TrimSpace(title) == "" {
		return nil
	}
	return failure
}

// AIR sends no recovery update, so output completes the active retry episode.
func (c *conversation) completeProviderFailure(turnID string, emit func(ports.ChatEvent)) {
	c.mu.Lock()
	if c.providerFailure == nil || c.providerFailure.ProviderTurnID != turnID {
		c.mu.Unlock()
		return
	}
	event := *c.providerFailure
	c.providerFailure = nil
	c.mu.Unlock()

	event.Kind = ports.ChatEventActivityCompleted
	event.ActivityStatus = domain.ActivityStatusCompleted
	emit(event)
}
