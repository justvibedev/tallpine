# tallpine report

Persist a meaningful worker report for durable delivery to the active project
orchestrator.

## Syntax

```text
tallpine report <free-form-text>
tallpine report --checkpoint --note <text> [output flags]
tallpine report --needs-input --note <text> [output flags]
tallpine report --stuck --note <text> [output flags]
tallpine report --done --note <text> [output flags]
```

Output flags are repeatable:

```text
--artifact <opaque-reference>
--pr-created <pr-or-mr-url>
--pr-reviewed <pr-or-mr-url>
```

Use reports for meaningful transitions, decisions, blockers, required input,
outputs, and terminal judgment. Do not file a report for routine commands. Outputs do not
imply completion, and `--done` does not terminate the session.

Attach a reviewable deliverable as soon as it is ready, not only at `--done`:
use `--artifact <reference>` on the report for the milestone that produced it.
Examples include a requested document, a rendered dashboard, or another output
the orchestrator or human needs to open directly. `--artifact` takes any opaque
reference string; it is not validated as a URL.
In a chat session, an HTML file from the artifact directory that you attach
with `--artifact` also shows in the thread.

Routine test logs, command output, scratch notes, and intermediate diagnostics
are working material, not deliverables. Do not attach them unless requested or
needed to explain an actionable failure. Summarize validation in the report note;
when a separate deliverable is useful, prefer one consolidated report over
individual logs. Ordinary progress updates and concise final answers do not
need separate artifact files.

Saving a local artifact or attaching its reference to a Tallpine report is not
external publishing authorization. Publish externally only within the
user-authorized scope.

## Deferring follow-ups

`--defer "<one line>"` files a follow-up you will not do in this task as an
**inbox** item in the project backlog, attributed to your session. Add it to a
report that has a state flag with `--note`, or to a free-form report:

```bash
tallpine report --done --note "Ready for review." \
  --pr-created https://github.com/owner/repo/pull/88 \
  --defer "Byte-based truncation in the approval card"
```

- Repeatable, at most 3 per report, each 1-200 characters.
- A line whose title (ignoring case and extra spaces) matches an open or inbox
  item is merged into it instead of filed twice.
- Each session may file at most 30 items and the project inbox holds at most
  50 untriaged ones. A line that hits a cap is not filed; the command prints
  `not filed: ...` for it, so put that text in your note instead.
- The orchestrator or the human triages the inbox. You cannot close, reorder or
  start items.

`--pr-created` and `--pr-reviewed` accept complete GitHub PR or GitLab MR URLs.

`--needs-input`, `--stuck`, `--done`, free-form messages, and any report
with an output (`--artifact`, `--pr-created`, `--pr-reviewed`), including a
checkpoint, are immediately eligible for delivery. No immediate flag is
needed. A `--checkpoint` with no output is progress only: it does not wake an idle orchestrator by itself. It rides along
with the next delivered batch or the orchestrator's next turn, and is delivered
on its own after at most ten minutes. Use `--needs-input` when you need a
response. An idle orchestrator wakes for immediate reports; busy Chat work keeps reports in the durable outbox
until admission, without forcing concurrent execution. `--stuck` additionally
requests a rate-limited interrupt. The five-minute done settlement grace only
informs missing-PR soft verification and does not delay delivery.

**Examples:**

```bash
tallpine report "The focused tests pass; I am checking the generated diff."
tallpine report --done --note "Ready for review." \
  --pr-created https://github.com/owner/repo/pull/88
```
