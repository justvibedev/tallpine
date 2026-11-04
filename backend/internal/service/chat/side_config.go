package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// frozenSideConfig contains the launch configuration that survives daemon recovery.
// It is never included in renderer-facing side summaries.
type frozenSideConfig struct {
	DataDir               string
	WorkspacePath         string
	Env                   map[string]string
	Permissions           domain.PermissionMode
	ReadOnly              bool
	SystemPrompt          string
	AdditionalDirectories []string
	MCPServers            []ports.ChatMCPServerConfig
}

// sessionPolicy is the agent policy decision the session's main conversation was
// started under. A side conversation restores or retries long after its frozen
// launch config was written, and that config carries no decision, so it takes
// the main chat's: otherwise a restored side chat would launch uncapped.
//
// It fails closed: with no start config (the main chat was stopped, or is not
// restored yet after a daemon restart) there is no decision to apply, and a side
// chat launched with the frozen mode and the zero Decision would run uncapped.
func (s *Service) sessionPolicy(session domain.SessionID) (agentpolicy.Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.startConfigs[domain.SessionConversationOwner(session)]
	if !ok {
		return agentpolicy.Decision{}, ErrNoController
	}
	return cfg.Policy, nil
}

func (s *Service) sideLaunchConfig(side domain.SideConversation) (StartConfig, ports.ChatDriver, error) {
	var frozen frozenSideConfig
	if err := json.Unmarshal(side.LaunchConfig, &frozen); err != nil {
		return StartConfig{}, nil, fmt.Errorf("read frozen side configuration: %w", err)
	}
	policy, err := s.sessionPolicy(side.SessionID)
	if err != nil {
		return StartConfig{}, nil, fmt.Errorf("side conversation %s has no main chat policy to launch under: %w", side.ID, err)
	}
	driver, err := s.drivers.Driver(side.Harness)
	if err == nil {
		err = admitChatFence(driver, side.Harness, policy, frozen.Permissions)
	}
	return StartConfig{Policy: policy, Harness: side.Harness, DataDir: frozen.DataDir, WorkspacePath: frozen.WorkspacePath, Env: frozen.Env, Permissions: frozen.Permissions, ReadOnly: frozen.ReadOnly, SystemPrompt: sideIdentityPrompt(frozen.SystemPrompt), AdditionalDirectories: frozen.AdditionalDirectories, MCPServers: frozen.MCPServers}, driver, err
}

// RenameSideChat updates a user-owned title without changing its context.
func (s *Service) RenameSideChat(ctx context.Context, session domain.SessionID, id, label string) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	if _, err := s.sides.ownedSide(ctx, session, id); err != nil {
		return err
	}
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 320 {
		return ErrSideQuestionInvalid
	}
	if err := s.sides.store.RenameSide(ctx, id, label, s.now()); err != nil {
		return err
	}
	s.sides.announce(id)
	return nil
}

// CancelQueuedSideQuestion cancels only one queued question.
func (s *Service) CancelQueuedSideQuestion(ctx context.Context, session domain.SessionID, id, turnID string) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	if _, err := s.sides.ownedSide(ctx, session, id); err != nil {
		return err
	}
	if err := s.sides.store.CancelSideTurn(ctx, id, turnID, s.now()); err != nil {
		return err
	}
	s.sides.announce(id)
	return nil
}

func (s *Service) closeSessionSides(ctx context.Context, session domain.SessionID) error {
	if s.sides == nil || s.sides.launchID() == "" {
		return nil
	}
	sides, err := s.sides.store.ListSideConversations(ctx, session, s.sides.launchID())
	if err != nil {
		return err
	}
	var failures []error
	for _, side := range sides {
		if err := s.closeSide(ctx, side); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
