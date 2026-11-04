package chat

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// slowRelayThreshold is the relay duration above which a send is logged with its
// phase timings, so a rare multi-minute `tallpine send` can be attributed to the
// controller gate (a start/stop/hibernate holding it) or to the turn dispatch.
const slowRelayThreshold = 5 * time.Second

// logSlowRelay warns when a relay (gate wait + dispatch) was slow. lockWait is
// everything before dispatch: the gate, the session read, and any fork,
// hibernate or wake wait inside workingController.
func (s *Service) logSlowRelay(id domain.SessionID, lockWait, dispatch time.Duration, err error) {
	if lockWait+dispatch < slowRelayThreshold {
		return
	}
	s.log.Warn("slow chat send", "session", id,
		"gateWait", lockWait.Round(time.Millisecond), "dispatch", dispatch.Round(time.Millisecond), "error", err)
}
