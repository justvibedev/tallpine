package sessionmanager

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// slowChatSendThreshold matches the chat service's slow-relay warning, so a
// slow `tallpine send` is attributed to the relay or to the manager's
// delivery fallback (resume queueing) that follows it.
const slowChatSendThreshold = 5 * time.Second

// logSlowChatSend warns with per-phase timings when a chat send was slow.
// relayDone is zero when the send never reached the relay.
func (m *Manager) logSlowChatSend(id domain.SessionID, started, relayDone time.Time) {
	end := m.clock()
	total := end.Sub(started)
	if total < slowChatSendThreshold {
		return
	}
	var relay, delivery time.Duration
	if !relayDone.IsZero() {
		relay, delivery = relayDone.Sub(started), end.Sub(relayDone)
	}
	m.logger.Warn("slow chat send (manager)", "sessionID", id,
		"total", total.Round(time.Millisecond), "relay", relay.Round(time.Millisecond),
		"delivery", delivery.Round(time.Millisecond))
}
