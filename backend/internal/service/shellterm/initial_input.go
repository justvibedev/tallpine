package shellterm

import (
	"context"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/terminalui"
)

// MatchInitialInputReadyState returns the first ready state whose marker text
// appears in output, or nil when the terminal has not reached any of them.
func MatchInitialInputReadyState(output string, readyStates []InitialInputReadyState) *InitialInputReadyState {
	for i := range readyStates {
		if strings.Contains(output, readyStates[i].Text) {
			return &readyStates[i]
		}
	}
	return nil
}

// sendInitialInputAfterTimeout delivers the reviewed input once the ready
// wait has expired without a marker, as long as the terminal is still alive.
func (s *Service) sendInitialInputAfterTimeout(handle ports.RuntimeHandle, input string) {
	ctx, cancel := context.WithTimeout(context.Background(), initialInputFallbackSendTimeout)
	defer cancel()
	alive, err := s.runtime.IsAlive(ctx, handle)
	if err != nil || !alive {
		s.log.Warn("authentication terminal exited before initial input", "handleId", handle.ID, "error", err)
		return
	}
	s.log.Info("authentication terminal showed no ready marker; sending initial input anyway", "handleId", handle.ID)
	if err := s.runtime.SendMessage(ctx, handle, input); err != nil {
		s.log.Warn("authentication terminal initial input failed", "handleId", handle.ID, "error", err)
	}
}

func (s *Service) sendInitialInputWhenReady(ctx context.Context, handle ports.RuntimeHandle, input string, readyStates []InitialInputReadyState, sendOnTimeout bool) {
	ctx, cancel := context.WithTimeout(ctx, s.initialInputTimeout)
	defer cancel()
	ticker := time.NewTicker(initialInputPollInterval)
	defer ticker.Stop()
	for {
		output, err := s.runtime.GetOutput(ctx, handle, initialInputOutputLines)
		if err == nil {
			ready := MatchInitialInputReadyState(output, readyStates)
			if styled, ok := s.runtime.(ports.StyledTerminalOutputReader); ok {
				if rendered, styledErr := styled.GetStyledOutput(ctx, handle, initialInputOutputLines); styledErr == nil {
					if renderedReady := MatchInitialInputReadyState(terminalui.PlainTerminalText(rendered), readyStates); renderedReady != nil {
						ready = renderedReady
					}
				}
			}
			if ready != nil && ready.SkipInput {
				return
			}
			if ready == nil {
				goto wait
			}
			if ready.RawPrefix != "" {
				if err := s.runtime.SendInput(ctx, handle, ready.RawPrefix); err != nil {
					s.log.Warn("authentication terminal initial input prefix failed", "handleId", handle.ID, "error", err)
					return
				}
			}
			if err := s.runtime.SendMessage(ctx, handle, input); err != nil {
				s.log.Warn("authentication terminal initial input failed", "handleId", handle.ID, "error", err)
			}
			return
		}
	wait:
		select {
		case <-ctx.Done():
			if sendOnTimeout {
				s.sendInitialInputAfterTimeout(handle, input)
				return
			}
			s.log.Warn("authentication terminal did not become ready for initial input", "handleId", handle.ID)
			return
		case <-ticker.C:
		}
	}
}
