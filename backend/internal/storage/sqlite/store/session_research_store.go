package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// SessionResearch reads an orchestrator's persisted research mode (B736);
// found is false when research mode is off or the session does not exist.
func (s *Store) SessionResearch(ctx context.Context, id domain.SessionID) (domain.SessionResearch, bool, error) {
	row, err := s.qr.GetSessionResearch(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !row.ResearchSince.Valid) {
		return domain.SessionResearch{}, false, nil
	}
	if err != nil {
		return domain.SessionResearch{}, false, fmt.Errorf("read research mode of %s: %w", id, err)
	}
	return domain.SessionResearch{
		Window:     row.ResearchWindow.String,
		Since:      row.ResearchSince.Time,
		LastTurnAt: row.ResearchLastTurnAt.Time,
	}, true, nil
}

// SetSessionResearch persists an orchestrator's research mode; nil clears it.
func (s *Store) SetSessionResearch(ctx context.Context, id domain.SessionID, research *domain.SessionResearch) error {
	arg := gen.SetSessionResearchParams{ID: id}
	if research != nil {
		arg.ResearchSince = sql.NullTime{Time: research.Since, Valid: true}
		arg.ResearchWindow = sql.NullString{String: research.Window, Valid: true}
		arg.ResearchLastTurnAt = sql.NullTime{Time: research.LastTurnAt, Valid: !research.LastTurnAt.IsZero()}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetSessionResearch(ctx, arg); err != nil {
		return fmt.Errorf("persist research mode of %s: %w", id, err)
	}
	return nil
}

// TouchSessionResearch records a finished turn of an orchestrator in research
// mode; it changes nothing while research mode is off.
func (s *Store) TouchSessionResearch(ctx context.Context, id domain.SessionID, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.TouchSessionResearch(ctx, gen.TouchSessionResearchParams{At: sql.NullTime{Time: at, Valid: true}, ID: id}); err != nil {
		return fmt.Errorf("record research turn of %s: %w", id, err)
	}
	return nil
}

// ClearAllSessionResearch ends every persisted research mode, for the global
// toggle turning off; a hibernated orchestrator has no controller to clear it.
func (s *Store) ClearAllSessionResearch(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.ClearAllSessionResearch(ctx); err != nil {
		return fmt.Errorf("clear all research modes: %w", err)
	}
	return nil
}
