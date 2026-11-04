package sessionmanager

// needsYouReplyPrompt tells every project's orchestrator to mark replies that need the
// human. The chat reads the marker (frontend WorkerUpdates.tsx asksUser) and draws such
// replies as full messages instead of the quiet footnote under a worker row.
const needsYouReplyPrompt = "## Replies That Need the Human\n\n" +
	"- When your reply to a worker update needs the human to act or answer (install and test something, say \"merge\", decide, unblock you), start the reply with the exact marker `**Needs you:**`. Chat shows marked replies as full messages; unmarked replies to worker updates are drawn as a quiet footnote. Never use the marker for plain status."
