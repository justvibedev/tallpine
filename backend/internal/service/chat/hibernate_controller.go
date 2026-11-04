package chat

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Provider catalog reads are passive: opening a chat must not wake it. Hold
// the same gate as hibernation so a provider cannot stop during the read.
func (s *Service) readingController(ctx context.Context, id domain.SessionID) (*Controller, domain.SessionRecord, func(), error) {
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if err := gate.lock(ctx); err != nil {
		return nil, domain.SessionRecord{}, nil, err
	}
	return s.admitReading(ctx, id, gate)
}

// tryReadingController is readingController that never waits for the gate: a
// start, stop, hibernate or switch in progress holds it for the whole provider
// call, and a plain read must not queue behind that.
func (s *Service) tryReadingController(ctx context.Context, id domain.SessionID) (*Controller, domain.SessionRecord, func(), error) {
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if !gate.tryLock() {
		return nil, domain.SessionRecord{}, nil, ErrControllerHandoff
	}
	return s.admitReading(ctx, id, gate)
}

// admitReading runs with gate held and releases it before returning.
func (s *Service) admitReading(ctx context.Context, id domain.SessionID, gate controllerGate) (*Controller, domain.SessionRecord, func(), error) {
	rec, err := s.requireChatSession(ctx, id)
	if err != nil {
		gate.unlock()
		return nil, domain.SessionRecord{}, nil, err
	}
	if rec.HibernatedAt != nil {
		gate.unlock()
		return nil, domain.SessionRecord{}, nil, ErrNoController
	}
	controller, err := s.Controller(id)
	if err != nil || controller.State() == ports.ChatControllerStopped {
		gate.unlock()
		return nil, domain.SessionRecord{}, nil, ErrNoController
	}
	release := controller.beginOperation()
	gate.unlock()
	return controller, rec, release, nil
}

// workingController admits provider work under the start/stop gate, then
// releases that gate so explicit Kill can interrupt a stuck provider call.
func (s *Service) workingController(ctx context.Context, id domain.SessionID) (*Controller, func(), error) {
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	for {
		if !gate.tryLock() {
			if controller, err := s.Controller(id); err == nil {
				controller.mu.Lock()
				handoff := controller.handoff
				controller.mu.Unlock()
				if handoff != controllerHandoffNone && handoff != controllerHandoffHibernate {
					return nil, nil, ErrControllerHandoff
				}
			}
			if err := gate.lock(ctx); err != nil {
				return nil, nil, err
			}
		}
		rec, err := s.requireChatSession(ctx, id)
		if err != nil {
			gate.unlock()
			return nil, nil, err
		}
		if rec.HibernatedAt == nil {
			controller, err := s.Controller(id)
			if err == nil {
				controller.mu.Lock()
				hibernating := controller.handoff == controllerHandoffHibernate
				stopped := controller.state == ports.ChatControllerStopped
				forkDone := controller.forkDone
				controller.mu.Unlock()
				if forkDone != nil {
					gate.unlock()
					select {
					case <-forkDone:
						continue
					case <-ctx.Done():
						return nil, nil, ctx.Err()
					}
				}
				if hibernating {
					gate.unlock()
					select {
					case <-controller.stopped:
						continue
					case <-ctx.Done():
						return nil, nil, ctx.Err()
					}
				}
				if !stopped {
					release := controller.beginOperation()
					gate.unlock()
					return controller, release, nil
				}
			}
			if !s.isWaking(id) {
				gate.unlock()
				return nil, nil, ErrNoController
			}
		}
		gate.unlock()
		if err := s.wakeHibernated(ctx, id); err != nil {
			return nil, nil, err
		}
	}
}
