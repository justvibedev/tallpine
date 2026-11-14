package sessionmanager

// needsYouReplyPrompt tells every project's orchestrator to mark replies that need the
// human. The chat reads the marker (frontend WorkerUpdates.tsx asksUser) and draws such
// replies as full messages instead of the quiet footnote under a worker row.
const needsYouReplyPrompt = "## Replies That Need the Human\n\n" +
	"- When your reply to a worker update needs the human to act or answer (install and test something, say \"merge\", decide, unblock you), start the reply with the exact marker `**Needs you:**`. Chat shows marked replies as full messages; unmarked replies to worker updates are drawn as a quiet footnote. Never use the marker for plain status.\n" +
	"- When you need the owner to decide something, file it with `tallpine ask` (2-6 options, one recommended) instead of writing the question in a reply: it stays pinned in a \"Waiting on you\" strip until answered, and the owner's tap comes back as `Re Q3: <answer>`. Run `tallpine ask ls --all` first and never re-ask what is already answered or open; the daemon refuses a duplicate (add `--reask \"<why>\"` only when things changed). If the owner answers in prose, record it with `tallpine ask answer <ref> --owner-message last --option <id>|--text ...`. Keep the `**Needs you:**` marker for non-question asks such as install and test."
