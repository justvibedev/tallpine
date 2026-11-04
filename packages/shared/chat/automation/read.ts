// What an automation message is, read from the text Tallpine wrote for it. Desktop and phone share
// this reader; each draws the result its own way.
import { parseBrowserAnnotationMessage, type ParsedBrowserAnnotationMessage } from "./browser-feedback";
import { readFileFeedback, type FileFeedbackAutomation } from "./file-feedback";
import { readPullRequestNudge, type PullRequestNudge } from "./pr-nudges";

export type { FailedCheck, PullRequestRef, ReviewComment } from "./pr-nudges";
export type { FileFeedback } from "./file-feedback";
export type GitAction = "createPR" | "createDraftPR" | "commit" | "commitPush" | "push";
export type AutomationNotice = "orchestratorReplaced" | "contextCarried" | "reviewTask";

export type Automation =
	| FileFeedbackAutomation
	| PullRequestNudge
	| { kind: "gitAction"; action: GitAction; base?: string }
	| { kind: "browserFeedback"; annotations: ParsedBrowserAnnotationMessage }
	| { kind: "notice"; notice: AutomationNotice };

function readUserAction(text: string): Automation | null {
	const feedback = readFileFeedback(text);
	if (feedback) return feedback;
	// The instructions SessionBranchSummary's git buttons send.
	const createPR = /^Commit any uncommitted changes with a clear message, push this branch, and open a (draft )?pull request against (?:`([^`]+)`|the default branch)\. Give it a descriptive title and summary\.$/.exec(text);
	if (createPR) return { kind: "gitAction", action: createPR[1] ? "createDraftPR" : "createPR", base: createPR[2] };
	if (text === "Commit all uncommitted changes with a clear message. Do not push.") return { kind: "gitAction", action: "commit" };
	if (/^Commit all uncommitted changes with a clear message and push this branch(?: to update PR #\d+)?\.$/.test(text)) return { kind: "gitAction", action: "commitPush" };
	if (/^Push this branch(?: to update PR #\d+)?\.$/.test(text)) return { kind: "gitAction", action: "push" };
	const annotations = parseBrowserAnnotationMessage(text);
	return annotations ? { kind: "browserFeedback", annotations } : null;
}

function readNotice(text: string): Automation | null {
	if (text.startsWith("Tallpine is replacing this project orchestrator. Stop coordinating new work now")) return { kind: "notice", notice: "orchestratorReplaced" };
	if (text.startsWith("Tallpine transferred the previous agent's context in hidden system instructions.")) return { kind: "notice", notice: "contextCarried" };
	if (/^Read and follow the Tallpine review task in `[^`]+`\.?$/.test(text)) return { kind: "notice", notice: "reviewTask" };
	return null;
}

/** What an automation message is, read from the text Tallpine wrote for it; null when it is none of the known kinds. */
export function readAutomation(text: string): Automation | null {
	const trimmed = text.trim();
	return readUserAction(trimmed) ?? readPullRequestNudge(trimmed) ?? readNotice(trimmed);
}
