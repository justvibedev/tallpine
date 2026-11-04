export const INVALIDATE_WINDOW_MS = 150;
// A session_updated that carries only the session id comes from usage
// bookkeeping (rarely an agent switch, cleanup fact or reported PR) and cannot
// say whether the board changed. During long agent runs these arrive faster than
// the window above and refetched the whole workspace every window for hours
// (#6763), so they refresh it at most once per this interval.
export const ID_ONLY_WORKSPACE_REFRESH_MS = 5_000;
// A session event names the session it changed, so the cached list takes that
// one session (GET /sessions/{id}) instead of refetching every session. A
// session is re-read at most this often under a stream of events.
export const SESSION_PATCH_MIN_INTERVAL_MS = 500;
// What still needs the whole list (an event naming no session, a PR moving
// between sessions) waits for a quiet moment: this long after the last such
// event, but never longer than the max wait after the first, so a busy stream
// refetches at most about once a second and never starves.
export const WORKSPACE_REFETCH_DEBOUNCE_MS = 1_000;
export const WORKSPACE_REFETCH_MAX_WAIT_MS = 5_000;
// Event types whose session the list can patch on its own. PR events are left
// out: a PR can move from one session to another, which a patch of the session
// the event names would miss.
export const SESSION_PATCH_EVENT_TYPES = new Set(["session_created", "session_updated", "review_run_created", "review_run_updated"]);
// EventSource.CLOSED, referenced numerically so test stubs without the static
// constants still work.
export const EVENTSOURCE_CLOSED = 2;

// CDC event types the daemon pushes over the SSE stream (see
// backend/internal/cdc/event.go). The SSE writer tags each frame with
// `event: <type>`, so named events bypass EventSource.onmessage and must be
// subscribed explicitly. Every one of these can change the project/session list
// the sidebar renders, so they all trigger a (batched) workspace refetch.
export const CDC_EVENT_TYPES = [
	"session_created",
	"session_updated",
	"pr_created",
	"pr_updated",
	"pr_check_recorded",
	"pr_session_changed",
	"pr_review_thread_added",
	"pr_review_thread_resolved",
	"review_run_created",
	"review_run_updated",
	"backlog_changed",
] as const;

// Live-only SSE frame the daemon sends when session status readiness changes.
// It replaces the old 300 ms "checking" poll (see workspaceRefetchInterval).
export const STATUS_READINESS_EVENT = "status_readiness_changed";
