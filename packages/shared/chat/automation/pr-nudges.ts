// Reads the PR nudges the daemon writes (lifecycle prIdentity / formatCIFailureMessage and the
// review-comment messages). Desktop and phone share this reader.

export type PullRequestRef = { number: number; title?: string; url?: string };
export type FailedCheck = { name: string; url?: string; log?: string };
export type ReviewComment = { path?: string; line?: number; author: string; body: string };

export type PullRequestNudge =
	| { kind: "ciFailing"; pr: PullRequestRef; checks: FailedCheck[] }
	| { kind: "reviewComments"; pr: PullRequestRef; comments: ReviewComment[] }
	| { kind: "changesRequested"; pr: PullRequestRef; author?: string; body: string }
	| { kind: "mergeConflict"; pr: PullRequestRef };

// `PR #12 "title" (source → target)`, as lifecycle's prIdentity writes it.
const PR_IDENTITY = /PR #(\d+)(?: ("(?:[^"\\\n]|\\.)*"))?(?: \([^\n]*? → [^\n]*?\))?/;
const PR_LINE = /\nPR: (\S+)\s*$/;
// A GitHub or GitLab login, or the daemon's stand-in. Never markup: it is interpolated into a translation.
const LOGIN = String.raw`[\w.-]+(?:\[bot\])?|unknown reviewer`;

function pullRequest(text: string, url?: string): PullRequestRef | undefined {
	const match = PR_IDENTITY.exec(text.split("\n", 1)[0]!);
	if (!match) return undefined;
	let title: string | undefined;
	if (match[2]) {
		try {
			title = JSON.parse(match[2]) as string;
		} catch {
			title = match[2].slice(1, -1);
		}
	}
	return { number: Number(match[1]), title, url };
}

// One check as lifecycle's formatCIFailureMessage writes it. The log tail's fence is
// longer than any backtick run inside it, so a log line cannot end the section.
const FAILED_CHECK = /\nFailed: ([^\n]*) \(([^()\n]*)\)(?:\nFailure URL: (\S+))?(?:\n\nLog tail \(last \d+ lines?\):\n(`{3,})\n([\s\S]*?)\n\4)?\n/y;

function failedChecks(text: string): FailedCheck[] | null {
	const checks: FailedCheck[] = [];
	let at = text.indexOf("\n") + 1;
	while (at > 0) {
		FAILED_CHECK.lastIndex = at;
		const match = FAILED_CHECK.exec(text);
		if (!match) break;
		checks.push({ name: match[1]!, url: match[3], log: match[5] });
		at = FAILED_CHECK.lastIndex;
	}
	return checks.length > 0 && text.startsWith("\nUse the included log tail", at) ? checks : null;
}

// Comment bodies are free text, so a body line can look like the next comment's
// heading. Headings must number 1..N for the N the message announces; anything
// else is not read as comments.
function reviewComments(text: string, announced: number): ReviewComment[] | null {
	const closing = text.lastIndexOf("\n\nAddress each comment and push fixes.");
	const headings = [...text.matchAll(new RegExp(String.raw`\n\n(\d+)\. ([^\n]+) \(@(${LOGIN})\):\n`, "g"))];
	if (closing < 0 || headings.length !== announced || headings.some((heading, index) => Number(heading[1]) !== index + 1)) return null;
	return headings.map((heading, index) => {
		const end = headings[index + 1]?.index ?? closing;
		const lines = text.slice(heading.index! + heading[0].length, end).replace(/\s+$/, "").split("\n");
		while (lines.length > 0 && /^ {3}(?:https?:\/\/\S+|Thread ID: .*)$/.test(lines.at(-1)!)) lines.pop();
		const at = heading[2] === "(general)" ? undefined : /^(.*?)(?::(\d+))?$/.exec(heading[2]!);
		return { path: at?.[1] || undefined, line: at?.[2] ? Number(at[2]) : undefined, author: heading[3]!, body: lines.join("\n").trim() };
	});
}

// Read from the end: the review URL and ID follow the free-text body.
function changesRequested(text: string): { body: string; url?: string } | null {
	const closing = text.lastIndexOf("\n\nAddress the requested changes and push.");
	if (closing < 0) return null;
	let rest = text.slice(0, closing);
	const id = /\nReview ID: [^\n]*$/.exec(rest);
	if (id) rest = rest.slice(0, id.index);
	const url = /\n\nReview URL: (\S+)$/.exec(rest);
	if (url) rest = rest.slice(0, url.index);
	const bodyAt = rest.indexOf("\n\nReview body:\n");
	return { body: bodyAt < 0 ? "" : rest.slice(bodyAt + "\n\nReview body:\n".length).trim(), url: url?.[1] };
}

export function readPullRequestNudge(text: string): PullRequestNudge | null {
	const prURL = PR_LINE.exec(text)?.[1];
	if (text.startsWith("There are merge conflicts on ")) {
		const pr = pullRequest(text, prURL);
		return pr ? { kind: "mergeConflict", pr } : null;
	}
	if (text.startsWith("CI is failing on ")) {
		const pr = pullRequest(text, prURL);
		const checks = pr ? failedChecks(text) : null;
		return pr && checks ? { kind: "ciFailing", pr, checks } : null;
	}
	const announced = /^The following (\d+) unresolved review comment\(s\) are on /.exec(text);
	if (announced) {
		const pr = pullRequest(text, prURL);
		const comments = pr ? reviewComments(text, Number(announced[1])) : null;
		return pr && comments ? { kind: "reviewComments", pr, comments } : null;
	}
	const reviewer = new RegExp(String.raw`^A changes-requested review from @(${LOGIN}) is on `).exec(text);
	if (reviewer) {
		const review = changesRequested(text.replace(PR_LINE, ""));
		const pr = review ? pullRequest(text, review.url ?? prURL) : undefined;
		return pr && review ? { kind: "changesRequested", pr, author: reviewer[1], body: review.body } : null;
	}
	// Tallpine's own reviewer, before its findings became GitHub reviews; only old history carries these.
	if (text.startsWith("[Tallpine reviewer] ")) {
		const url = /^PR: (\S+)$/m.exec(text)?.[1];
		const number = Number(/\/pull\/(\d+)/.exec(url ?? "")?.[1]);
		if (!url || !number) return null;
		const body = /\n\nReview body:\n([\s\S]*?)(?=\n\nReview \d+\n|$)/.exec(text)?.[1] ?? "";
		return { kind: "changesRequested", pr: { number, url }, body: body.trim() };
	}
	return null;
}

/** The forges Tallpine reads PRs from. A URL in the text links only to these, so a look-alike message cannot send the user elsewhere. */
export function forgeURL(url: string | undefined): string | undefined {
	if (!url) return undefined;
	try {
		const { protocol, hostname } = new URL(url);
		return protocol === "https:" && (hostname === "github.com" || hostname === "gitlab.com") ? url : undefined;
	} catch {
		return undefined;
	}
}
