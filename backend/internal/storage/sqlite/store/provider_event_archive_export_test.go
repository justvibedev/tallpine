package store

import "context"

// ArchiveDumpForTest returns every provider-event archive row as SQL literals,
// each column with its stored type and bytes, in id order.
func (s *Store) ArchiveDumpForTest(ctx context.Context) ([]string, error) {
	rows, err := s.writeDB.QueryContext(ctx, `SELECT quote(id)||'|'||quote(conversation_id)||'|'||quote(session_id)||'|'||
    quote(provider_event_id)||'|'||quote(method)||'|'||quote(payload_json)||'|'||quote(received_at)||'|'||
    quote(branch_id)||'|'||quote(review_id)||'|'||typeof(received_at)
FROM conversation_provider_events ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ExecWriterForTest runs one statement on the writer connection.
func (s *Store) ExecWriterForTest(ctx context.Context, query string, args ...any) error {
	_, err := s.writeDB.ExecContext(ctx, query, args...)
	return err
}

// ProviderEventKeysForTest lists the identity keys kept for a conversation.
func (s *Store) ProviderEventKeysForTest(ctx context.Context, conversationID string) ([]string, error) {
	rows, err := s.writeDB.QueryContext(ctx, `SELECT provider_event_id FROM conversation_provider_event_keys
WHERE conversation_id = ? ORDER BY provider_event_id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
