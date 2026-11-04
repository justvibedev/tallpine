package store

import (
	"context"
	"fmt"
)

// LiveBacklogArtifactRefs returns what the live (inbox or open) backlog items
// point at, so artifact retention can leave those session directories alone:
// the ids of sessions linked to them, the refs of their artifact links, and
// their free text (note and agent notes), where design docs are referenced by
// path such as "agent-orchestrator-494/design.md".
func (s *Store) LiveBacklogArtifactRefs(ctx context.Context) (sessions, artifacts, texts []string, err error) {
	rows, err := s.readDB.QueryContext(ctx, `
SELECT l.kind, l.ref FROM backlog_links l
JOIN backlog_items i ON i.id = l.item_id
WHERE i.state IN ('inbox', 'open') AND l.kind IN ('session', 'artifact')`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("live backlog links: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind, ref string
		if err := rows.Scan(&kind, &ref); err != nil {
			return nil, nil, nil, fmt.Errorf("scan live backlog link: %w", err)
		}
		if kind == "session" {
			sessions = append(sessions, ref)
		} else {
			artifacts = append(artifacts, ref)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	trows, err := s.readDB.QueryContext(ctx, `
SELECT note, agent_notes FROM backlog_items WHERE state IN ('inbox', 'open')`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("live backlog notes: %w", err)
	}
	defer func() { _ = trows.Close() }()
	for trows.Next() {
		var note, agent string
		if err := trows.Scan(&note, &agent); err != nil {
			return nil, nil, nil, fmt.Errorf("scan live backlog notes: %w", err)
		}
		texts = append(texts, note, agent)
	}
	return sessions, artifacts, texts, trows.Err()
}
