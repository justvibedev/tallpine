package migratevo

// ColumnRef names one table column.
type ColumnRef struct{ Table, Column string }

// PathColumns hold absolute filesystem paths that may live under the source
// root. The rewrite is prefix-only: a value changes only if it starts with the
// old root. Missing tables/columns are skipped so older schemas still migrate.
var PathColumns = []ColumnRef{
	{"sessions", "workspace_path"},
	{"sessions", "artifact_dir"},
	{"sessions", "workspace_repo_path"},
	{"sessions", "native_transcript_path"},
	{"report_outputs", "reference"},
	{"session_worktrees", "worktree_path"},
	{"shell_terminals", "working_dir"},
	{"agent_native_sessions", "config_dir"},
	{"agent_native_sessions", "transcript_path"},
	{"agent_switches", "agent_handoff_path"},
	{"agent_switches", "final_handoff_path"},
	{"usage_sources", "artifact_path"},
	{"session_artifact_versions", "path"},
	{"projects", "path"},
	{"agent_accounts", "home"},
}

// NotRewritten are path-named columns that deliberately stay as they are:
// URLs, repo-relative paths and identifiers. The schema test requires every
// path-named TEXT column to be in PathColumns or here, so a new path column
// cannot slip through unnoticed.
var NotRewritten = []ColumnRef{
	{"sessions", "preview_url"},
	{"pr", "url"}, {"pr", "html_url"}, {"pr", "author_avatar_url"}, {"pr", "changed_files"},
	{"pr_checks", "pr_url"}, {"pr_checks", "url"},
	{"pr_comment", "pr_url"}, {"pr_comment", "file"}, {"pr_comment", "url"},
	{"pr_review_threads", "pr_url"}, {"pr_review_threads", "path"},
	{"workspace_repos", "relative_path"}, {"workspace_repos", "repo_origin_url"},
	{"review_run", "pr_url"}, {"pr_reviews", "pr_url"}, {"pr_reviews", "url"},
	{"projects", "repo_origin_url"}, {"review", "pr_url"},
	{"pr_url_alias", "alias_url"}, {"pr_url_alias", "canonical_url"},
	{"usage_bindings", "native_root_id"}, {"usage_sources", "file_identity"},
	{"backlog_reopen_acks", "pr_url"}, {"notifications", "pr_url"}, {"pr_paths", "pr_url"}, {"pr_paths", "path"},
}

// HistoryColumns are free-text columns (prompts, transcripts, reports, command
// output) that quote old paths in prose. They are history and stay unchanged;
// the scan test tolerates old-prefix hits only here.
var HistoryColumns = []ColumnRef{
	{"sessions", "prompt"}, {"sessions", "latest_user_prompt"},
	{"pr_comment", "body"}, {"review_run", "body"}, {"pr_reviews", "body"},
	{"conversation_messages", "text"},
	{"conversation_provider_events", "payload_json"},
	{"conversation_activities", "summary"}, {"conversation_activities", "detail_json"},
	{"conversation_activities", "command_output"}, {"conversation_activities", "streamed_text"},
	{"conversation_turns", "diff_json"}, {"conversation_turns", "plan_json"},
	{"conversation_turns", "provider_input_text"},
	{"conversation_steer_deliveries", "request_json"},
	{"reports", "note"}, {"reports", "message"},
	{"session_last_assistant_messages", "message"},
	{"cleanup_runs", "report"}, {"turn_fallbacks", "excerpt"},
	{"backlog_items", "note"}, {"backlog_items", "agent_notes"},
}
