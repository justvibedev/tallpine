package store

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// maxToolActivityRows bounds one doc-routing read; the newest rows win.
const maxToolActivityRows = 20000

// ToolActivity is the part of one command or tool row that says what a session
// looked at: the fields a harness stores, pulled out of the detail JSON so a
// large output or diff is never read.
type ToolActivity struct {
	SessionID domain.SessionID
	Kind      domain.ActivityKind
	// ToolKind is the ACP tool kind ("read", "search"), "" for other rows.
	ToolKind string
	// ProviderToolName is the provider's own tool name ("Grep", "Glob") when it
	// reports one, "" otherwise.
	ProviderToolName string
	// Locations is the ACP location list as JSON, "" when there is none.
	Locations string
	// Command is the command text a codex-style row carries.
	Command string
	// Input is the tool input as JSON, "" when there is none.
	Input     string
	CreatedAt time.Time
}

// ListToolActivity returns the command and tool rows of the project's sessions
// since the given time, newest first. A row with unreadable detail comes back
// with empty detail fields.
func (s *Store) ListToolActivity(ctx context.Context, project domain.ProjectID, since time.Time) ([]ToolActivity, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT c.session_id, a.kind, a.created_at,
    CASE WHEN json_valid(a.detail_json) THEN COALESCE(json_extract(a.detail_json, '$.toolKind'), '') ELSE '' END,
    CASE WHEN json_valid(a.detail_json) THEN COALESCE(json_extract(a.detail_json, '$.providerToolName'), '') ELSE '' END,
    CASE WHEN json_valid(a.detail_json) THEN COALESCE(json_extract(a.detail_json, '$.locations'), '') ELSE '' END,
    CASE WHEN json_valid(a.detail_json) THEN COALESCE(json_extract(a.detail_json, '$.command'), '') ELSE '' END,
    CASE WHEN json_valid(a.detail_json) THEN COALESCE(json_extract(a.detail_json, '$.input'), '') ELSE '' END
FROM conversation_activities a
JOIN conversations c ON c.id = a.conversation_id AND c.scope = 'session'
JOIN sessions s ON s.id = c.session_id
WHERE s.project_id = ? AND a.created_at >= ? AND a.kind IN ('command', 'mcp_tool')
ORDER BY a.created_at DESC, a.sequence DESC LIMIT ?`, string(project), since, maxToolActivityRows)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ToolActivity
	for rows.Next() {
		var t ToolActivity
		var session, kind string
		if err := rows.Scan(&session, &kind, &t.CreatedAt, &t.ToolKind, &t.ProviderToolName, &t.Locations, &t.Command, &t.Input); err != nil {
			return nil, err
		}
		t.SessionID, t.Kind = domain.SessionID(session), domain.ActivityKind(kind)
		out = append(out, t)
	}
	return out, rows.Err()
}

// CountActiveSessions counts the project's sessions updated since the given
// time, the base a routing finding's tool-activity coverage is measured against.
func (s *Store) CountActiveSessions(ctx context.Context, project domain.ProjectID, since time.Time) (int, error) {
	var n int
	err := s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE project_id = ? AND updated_at >= ?`, string(project), since).Scan(&n)
	return n, err
}
