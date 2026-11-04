package settings

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// dynamicContextStore is the optional store ability to save the dynamic
// orchestrator context toggle.
type dynamicContextStore interface {
	SetDynamicOrchestratorContext(ctx context.Context, enabled bool, now time.Time) error
}

// SetDynamicContextHook sets what runs after the toggle is saved; call before
// the service is shared.
func (s *Service) SetDynamicContextHook(fn func(ctx context.Context, enabled bool)) {
	s.onDynamicContext = fn
}

// DynamicOrchestratorContextEnabled reports the global toggle for orchestrator
// research mode. An unreadable store counts as the default, on: research only
// raises a window an orchestrator asks for.
func (s *Service) DynamicOrchestratorContextEnabled(ctx context.Context) bool {
	snapshot, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return true
	}
	return s.applyManaged(snapshot).DynamicOrchestratorContext
}

// SetDynamicOrchestratorContext changes the global toggle for orchestrator
// research mode. Turning it off ends every running research mode.
func (s *Service) SetDynamicOrchestratorContext(ctx context.Context, enabled bool) (Snapshot, error) {
	if s.managed.Locked(dynamicContextKey) {
		return Snapshot{}, s.managed.Refuse(dynamicContextKey)
	}
	st, ok := s.store.(dynamicContextStore)
	if !ok {
		return Snapshot{}, ports.ErrChatUnsupported
	}
	if err := st.SetDynamicOrchestratorContext(ctx, enabled, s.now()); err != nil {
		return Snapshot{}, err
	}
	if s.onDynamicContext != nil {
		s.onDynamicContext(ctx, s.DynamicOrchestratorContextEnabled(ctx))
	}
	return s.Get(ctx)
}
