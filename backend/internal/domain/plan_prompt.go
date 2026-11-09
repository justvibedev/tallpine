package domain

// PlanFormatPrompt is the part of a planning brief shared by the separate
// planner and a plan-in-place implementer (B335): how to find the open
// decisions, the questions file, and the plan, decisions, PR split and focus
// files. The planner's standing prompt is this text between its own opening and
// closing sentences; changing it changes both.
const PlanFormatPrompt = "### Explore, then find the open decisions\n\n" +
	"Before deciding anything, explore the repo. Then go through these lenses, using only the ones that apply to this request and this codebase, to find decisions the request leaves open:\n" +
	"- who or what uses it (people, other services, callers) and what they observe\n" +
	"- states and lifecycle of anything created or changed, and what triggers each change\n" +
	"- constraints and limits, and how the items involved relate to each other\n" +
	"- failure and misuse\n" +
	"- concurrency and time\n" +
	"- compatibility with existing data, behaviour and interfaces\n" +
	"- what is out of scope\n" +
	"Settle what the code answers yourself. For the rest, decide a sensible default yourself unless getting it wrong would be expensive.\n\n" +
	"### Questions (once)\n\n" +
	"Submit ONE questions file with `tallpine plan questions <item ref> --file questions.json`, then stop and wait: the answers arrive as a message. " +
	"The file is JSON: {\"questions\":[{\"id\",\"text\",\"reason\",\"options\":[{\"id\",\"label\"}],\"recommended\"}],\"assumptions\":[{\"id\",\"text\",\"scopeCut\"?}]}.\n" +
	"- Questions: at most 5. Only decisions that are expensive to get wrong, that the code cannot answer, and that have no obvious default. " +
	"Give each question 2 to 6 options, a recommended option (its id), and a one-line reason. Question text 500 characters or fewer.\n" +
	"- Assumptions: every other decision you made that the owner might care about, one line each (\"Assumed: ...\"), so the owner can skim the list and correct any of them. " +
	"Every rule about who can do what, when, and what users see (visibility, permissions, timing, ordering, limits, grouping) must be a question or an assumption the owner can correct, never a silent plan decision. Rules you only find while writing the plan go in the decisions table marked 'New, not confirmed by owner', so the owner sees them before approving. Any decision that differs from an owner answer must say 'overrides owner answer' in its row. At most 20 assumptions; if over, merge related ones. 300 characters or fewer each. Set \"scopeCut\":true on any assumption that drops or postpones scope (something the request seems to ask for that this plan leaves out or defers); omit it otherwise.\n" +
	"Do not pad either list. Fewer, sharper items are better. With no questions and no assumptions, submit empty lists and the plan can follow at once.\n\n" +
	"### The plan\n\n" +
	"After the answers, submit the plan with `tallpine plan submit <item ref> --plan plan.md --decisions decisions.md --prs prs.json --focus focus.json`. " +
	"plan.md has these sections: goal and non-goals; decisions (a table of question or assumption to final answer: owner answer, correction or accepted default); data model and migration; interfaces per user or caller; lifecycle; side effects and failure handling; PR split; risks. " +
	"Use only the sections that apply. decisions.md is that decisions table alone (8000 bytes or fewer); the builder and the reviewer read it. " +
	"prs.json is the PR split: [{\"title\",\"files\":[],\"tests\",\"summary\",\"consumes\":[],\"produces\":[]}]. Each PR entry has an Interfaces block: " +
	"Consumes (field consumes) lists the exact names and signatures it needs from earlier PRs; Produces (field produces) lists the exact names later PRs rely on; at least one of the two must be non-empty. " +
	"focus.json is a JSON list of at most 5 strings: the failure modes most likely to cause harm, which the reviewer checks hardest.\n" +
	"PR split: the default is ONE PR, and a small or medium feature is one PR. Split into several only when a single PR would be too large to review well, or when the parts are independently usable. When you do split, cut vertical slices (each PR delivers a working end-to-end piece), never horizontal layers such as schema, then API, then UI.\n" +
	"Existing code: plan.md must include an \"Existing code\" inventory. For each step, list the existing modules, helpers, models or clients it reuses or extends (file paths), name any duplicates you found in that area, and justify each new abstraction. A plan that adds a second implementation of existing logic must say why.\n" +
	"Write no step-by-step TDD scripts and no code in the plan.\n\n" +
	"### Before you submit, check\n\n" +
	"(a) every requirement maps to a PR; (b) names and signatures stay consistent across PRs; (c) proportion: a plan much longer than the spec is a transcript, not a plan, so cut it.\n\n"
