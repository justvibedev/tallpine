# tallpine ask

Ask the owner a question that stays visible until they answer: a tracked ask.
It appears in a pinned "Waiting on you" strip above the composer of the chat
that asked, as a card inline on the turn that asked, and as an owner
notification. Only orchestrators ask; a worker reports to its orchestrator.

```bash
tallpine ask ls --all
tallpine ask "Use Postgres for the seat counts?" \
  --option a="Yes, Postgres" --option b="No, keep SQLite" --recommended a \
  --reason "Both work; Postgres needs a migration" --about B62
tallpine ask answer Q3 --option a --owner-message last
tallpine ask withdraw Q3 --reason "B62 was dropped"
```

## Rules

- Use `tallpine ask` for every question you need the owner to decide. Do not
  put a decision in prose and hope they see it. Keep `**Needs you:**` for
  asks that are not questions (install and test something, say "merge", unblock
  you).
- Run `tallpine ask ls --all` first. Every orchestrator of the project shares
  these asks. The daemon refuses a question that matches an open ask or an
  answered one (same `--about` key, or the same text ignoring case and
  punctuation) and prints the earlier ask and answer: use that answer.
- If things changed since the owner answered, ask again with
  `--reask "<why>"`; the card then shows "asked again: <why>" and the earlier
  answer. A match with an open ask is always refused, `--reask` or not: wait.
- 2 to 6 options, each `id=label`, one `--recommended` id, question text up to
  500 characters. At most 10 asks are open per chat.
- The owner answers with a tap on the card; that lands in your chat as the
  owner's own message `Re Q3: <answer>`. Act on it. If they answer in prose
  instead, record it with
  `tallpine ask answer Q3 --option <id>|--text "<their words>" --owner-message last`
  (or a message id). The daemon checks it is the owner's and newer than the
  ask. Never answer for the owner.
- An ask belongs to the project and lane, not to your session: after a restart
  the new orchestrator of that lane still sees it and receives its answer.
  Ending a lane withdraws its open asks. Withdraw an ask yourself when it stops
  mattering; asks never expire on a timer.
- An answer or withdrawal on a closed ask fails with `ASK_NOT_OPEN`; the first
  answer wins.
