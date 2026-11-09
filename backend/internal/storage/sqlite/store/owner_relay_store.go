package store

import (
	"context"
	"database/sql"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// ownerLastScan bounds how far back "last" looks for the owner's own turn
// past relayed and [auto] lines.
const ownerLastScan = 50

// OwnChatMessage looks up ref in session's own chat: the conversation the
// session owns or the project conversation it currently controls. ref is a
// message id, or "last" for the owner's newest turn there, which is returned
// even when it cannot be verified: a relay must never fall back to an older
// approval. Text excludes any attached worker reports. ok is false when no such
// message is in that chat; the caller still checks the turn with
// IsGenuineOwnerTurn.
func (s *Store) OwnChatMessage(ctx context.Context, session domain.SessionID, ref string) (domain.OwnerChatMessage, bool, error) {
	if ref == "last" {
		rows, err := s.qr.SelectRecentOwnChatHumanMessages(ctx, gen.SelectRecentOwnChatHumanMessagesParams{SessionID: &session, MaxRows: ownerLastScan})
		if err != nil {
			return domain.OwnerChatMessage{}, false, err
		}
		for _, r := range rows {
			m := domain.OwnerChatMessage{ID: r.ID, Role: r.Role, Origin: r.Origin, SenderSessionID: r.SenderSessionID, Text: domain.StripWorkerReports(r.Text), At: r.CreatedAt, SpawnBrief: r.SpawnBrief != 0}
			if !m.IsMachineAuthored() {
				return m, true, nil
			}
		}
		return domain.OwnerChatMessage{}, false, nil
	}
	r, err := s.qr.SelectOwnChatMessage(ctx, gen.SelectOwnChatMessageParams{MessageID: ref, SessionID: &session})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OwnerChatMessage{}, false, nil
	}
	if err != nil {
		return domain.OwnerChatMessage{}, false, err
	}
	return domain.OwnerChatMessage{ID: r.ID, Role: r.Role, Origin: r.Origin, SenderSessionID: r.SenderSessionID, Text: domain.StripWorkerReports(r.Text), At: r.CreatedAt, SpawnBrief: r.SpawnBrief != 0}, true, nil
}
