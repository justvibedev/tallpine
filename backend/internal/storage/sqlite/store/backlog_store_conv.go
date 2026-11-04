package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

func backlogItemFromGen(row gen.BacklogItem) (domain.BacklogItem, error) {
	fields := map[string]string{}
	if row.Fields != "" {
		if err := json.Unmarshal([]byte(row.Fields), &fields); err != nil {
			return domain.BacklogItem{}, fmt.Errorf("decode backlog fields for %s: %w", row.ID, err)
		}
	}
	item := domain.BacklogItem{
		ID:           row.ID,
		ProjectID:    domain.ProjectID(row.ProjectID),
		Seq:          row.Seq,
		Title:        row.Title,
		Note:         row.Note,
		DoneWhen:     row.DoneWhen,
		State:        domain.BacklogState(row.State),
		Hold:         row.Hold,
		Scope:        domain.BacklogScope(row.Scope.String),
		ParentID:     row.ParentID.String,
		Position:     row.Position,
		Origin:       domain.BacklogOrigin{Kind: domain.BacklogOriginKind(row.OriginKind), SessionID: row.OriginSessionID, Excerpt: row.OriginExcerpt},
		AgentNotes:   row.AgentNotes,
		Fields:       fields,
		ClosedReason: row.ClosedReason.String,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
	if row.ClosedAt.Valid {
		t := row.ClosedAt.Time
		item.ClosedAt = &t
	}
	return item, nil
}

func backlogItemsFromGen(rows []gen.BacklogItem) ([]domain.BacklogItem, error) {
	out := make([]domain.BacklogItem, 0, len(rows))
	for _, row := range rows {
		item, err := backlogItemFromGen(row)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func fieldsJSON(fields map[string]string) (string, error) {
	if len(fields) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
