package sqlite

import (
	"strings"
	"testing"
)

// Break caught: the queue drain after every Chat turn joined queued turns to
// their user message by turn_id with no index, scanning every
// conversation_message ever recorded on the writer. Mirrors
// SelectNextQueuedConversationTurn, ListHibernatedChatsWithQueuedTurns and
// AttachLegacyCompactionsToRollbackAnchor.
func TestQueueDrainUsesIndexes(t *testing.T) {
	db := openMigratedTestDB(t)
	plan := func(query string, args ...any) string {
		t.Helper()
		rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var lines []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(lines, "\n")
	}

	next := plan(`SELECT conversation_turns.id, conversation_messages.text
FROM conversation_turns
JOIN conversation_messages
    ON conversation_messages.turn_id = conversation_turns.id
    AND conversation_messages.role = 'user'
WHERE conversation_turns.conversation_id = ?
  AND conversation_turns.state = 'queued'
  AND conversation_turns.promotion_started_at IS NULL
ORDER BY conversation_turns.requested_at, conversation_turns.rowid
LIMIT 1`, "c-1")
	for _, want := range []string{"INDEX idx_conversation_turns_queued", "INDEX idx_conversation_messages_user_turn"} {
		if !strings.Contains(next, want) {
			t.Fatalf("queue head does not use %s:\n%s", want, next)
		}
	}
	if strings.Contains(next, "SCAN ") {
		t.Fatalf("queue head scans a table:\n%s", next)
	}

	hibernated := plan(`SELECT DISTINCT sessions.id FROM sessions
JOIN conversations ON conversations.current_session_id = sessions.id
JOIN conversation_turns ON conversation_turns.conversation_id = conversations.id
WHERE sessions.session_mode = 'chat' AND sessions.is_terminated = 0
    AND sessions.hibernated_at IS NOT NULL
    AND conversation_turns.state = 'queued'
    AND conversation_turns.promotion_started_at IS NULL
ORDER BY sessions.id`)
	if !strings.Contains(hibernated, "INDEX idx_conversation_turns_queued") {
		t.Fatalf("hibernated queue sweep does not use idx_conversation_turns_queued:\n%s", hibernated)
	}

	anchor := plan(`UPDATE conversation_activities
SET turn_id = ?1, revision = revision + 1, updated_at = ?2
WHERE conversation_activities.conversation_id = ?3
  AND turn_id IS NULL
  AND kind = 'system'
  AND json_extract(detail_json, '$.event') = 'compaction'
  AND sequence >= COALESCE((
      SELECT MIN(sequence) FROM (
          SELECT anchor_message.sequence FROM conversation_messages AS anchor_message
          WHERE anchor_message.conversation_id = ?3 AND anchor_message.turn_id = ?1
          UNION ALL
          SELECT anchor_activity.sequence FROM conversation_activities AS anchor_activity
          WHERE anchor_activity.conversation_id = ?3 AND anchor_activity.turn_id = ?1
      )
  ), 0)`, "t-1", "now", "c-1")
	if strings.Contains(anchor, "SCAN anchor_") || strings.Contains(anchor, "SCAN conversation_") {
		t.Fatalf("rollback anchor lookup scans a table:\n%s", anchor)
	}
}
