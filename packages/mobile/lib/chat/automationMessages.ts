/**
 * Tallpine's automation messages and worker reports, read back from the fixed text Tallpine
 * writes for each, so the timeline can draw each as what it is instead of one
 * generic "automation" card.
 *
 * A port of the desktop's readers (frontend/src/renderer/components/chat/
 * AutomationMessage.tsx and WorkerUpdates.tsx). Keep the two in step: a change
 * to an Tallpine message template needs its reader changed in both places.
 */
// ponytail: parses the daemon's prompt text, as the desktop does; structured rows on the message would end the guess on both clients.

import { SENDER_PREFIX, parseViaPrefix, stripSenderPrefix as stripSenderPrefixShared } from "@tallpine/shared/chat/automation/sender-prefix";
import { foldedReportsEnvelope, humanVisibleText, parseReports, splitArtifacts, splitHeadline, type WorkerUpdate } from "@tallpine/shared/chat/automation/worker-reports";
import { parseBrowserAnnotationMessage, type ParsedBrowserAnnotationItem, type ParsedBrowserAnnotationMessage } from "@tallpine/shared/chat/automation/browser-feedback";
import { readAutomation, type Automation, type AutomationNotice, type GitAction } from "@tallpine/shared/chat/automation/read";
import { cachedBy as sharedCachedBy, latestWorkerUpdateKeys, piggybackedUpdates, workerUpdatesOf } from "@tallpine/shared/chat/automation/worker-updates";
import { linkRegExp } from "../brand";
import type { ConversationItem } from "./types";

/* -------------------------------------------------------------------------- */
/* worker reports                                                              */
/* -------------------------------------------------------------------------- */

export type { WorkerUpdate };

const cachedBy: <T>(read: (item: ConversationItem) => T) => (item: ConversationItem) => T = sharedCachedBy;

export { latestWorkerUpdateKeys, piggybackedUpdates, workerUpdatesOf, humanVisibleText, parseReports, splitArtifacts, splitHeadline };
export const stripSenderPrefix = stripSenderPrefixShared;

/** The outside tool a human-origin message came through (`[via grok-bot]`) and what it said. */
export const relayedVia = cachedBy((item): { name: string; body: string } | null =>
	item.kind === "message" && item.role === "user" && !item.senderSessionId ? parseViaPrefix(humanVisibleText(item.text)) : null);

/* -------------------------------------------------------------------------- */
/* automation messages                                                         */
/* -------------------------------------------------------------------------- */

export type { Automation, AutomationNotice, FailedCheck, FileFeedback, GitAction, PullRequestRef, ReviewComment } from "@tallpine/shared/chat/automation/read";
export { readAutomation } from "@tallpine/shared/chat/automation/read";

export type BrowserAnnotation = ParsedBrowserAnnotationItem;
export type BrowserFeedback = ParsedBrowserAnnotationMessage;
export const parseBrowserFeedback = parseBrowserAnnotationMessage;

export const automationOf = cachedBy((item): Automation | null =>
	item.kind === "message" && item.role === "user" && item.origin !== "human" && !item.senderSessionId && !/^\s*\[from /.test(item.text)
		? readAutomation(item.text)
		: null,
);

import { forgeURL } from "@tallpine/shared/chat/automation/pr-nudges";
export { forgeURL };

export const GIT_ACTION_LABELS: Record<GitAction, string> = {
	createPR: "Create PR",
	createDraftPR: "Create draft PR",
	commit: "Commit",
	commitPush: "Commit & push",
	push: "Push",
};

/** Names an author-less changes-requested review (old findings from the built-in reviewer). */
export const REVIEWER_LABEL = "Reviewer";

export const NOTICE_LABELS: Record<AutomationNotice, string> = {
	orchestratorReplaced: "This orchestrator is being replaced. A fresh one takes over in a new workspace.",
	contextCarried: "Context carried over from the previous agent",
	reviewTask: "Gave this reviewer its review task",
};

/**
 * Where a reported artifact sits in its session's artifact folder, the path the
 * daemon's artifact route serves. Reports name artifacts absolutely
 * (`…/artifacts/<session>/report.md`) or relative to that folder; a path outside
 * it has no entry and is not opened.
 */
export function artifactEntry(path: string, sessionId: string | undefined): string | undefined {
	const trimmed = path.trim().replace(/\\/g, "/");
	if (!trimmed || !sessionId) return undefined;
	let entry = trimmed;
	if (trimmed.startsWith("/") || /^[A-Za-z]:\//.test(trimmed)) {
		const marker = `/artifacts/${sessionId}/`;
		const at = trimmed.lastIndexOf(marker);
		if (at < 0) return undefined;
		entry = trimmed.slice(at + marker.length);
	}
	// The entry is joined into an authenticated daemon URL, so it may only name
	// something inside the folder: no parent steps, whichever form the path took.
	const segments = entry.split("/").filter((segment) => segment !== "" && segment !== ".");
	return segments.length > 0 && !segments.includes("..") ? segments.join("/") : undefined;
}

export type InlineRun = { text: string; href?: string; code?: boolean; strong?: boolean };

// A markdown link, a bare web or session link, inline code, or bold: the inline
// markup workers put in reports. Trailing punctuation stays out of a bare link.
const INLINE = linkRegExp(String.raw`\[([^\]\n]+)\]\(([^)\s]+)\)|(https?:\/\/[^\s<>()]*[^\s<>().,;:!?'"])|((?:@SCHEME@):\/\/sessions\/[^\s<>()]*[^\s<>().,;:!?'"])|\x60([^\x60\n]+)\x60|\*\*([^*\n]+)\*\*`, "g");

/** A report's text as runs to draw, its links kept as links. */
export function inlineRuns(text: string): InlineRun[] {
	const runs: InlineRun[] = [];
	let at = 0;
	for (const match of text.matchAll(INLINE)) {
		if (match.index! > at) runs.push({ text: text.slice(at, match.index) });
		if (match[1] !== undefined) runs.push({ text: match[1], href: match[2] });
		else if (match[3] !== undefined || match[4] !== undefined) runs.push({ text: match[0], href: match[0] });
		else if (match[5] !== undefined) runs.push({ text: match[5], code: true });
		else runs.push({ text: match[6]!, strong: true });
		at = match.index! + match[0].length;
	}
	if (at < text.length) runs.push({ text: text.slice(at) });
	return runs;
}

/** The project and session an `ao://sessions/<project>/<session>` link names. */
export function sessionLink(href: string): { projectId: string; sessionId: string } | undefined {
	const match = linkRegExp(String.raw`^(?:@SCHEME@):\/\/sessions\/([^/?#\s]+)\/([^/?#\s]+)\/?$`).exec(href);
	if (!match) return undefined;
	try {
		return { projectId: decodeURIComponent(match[1]!), sessionId: decodeURIComponent(match[2]!) };
	} catch {
		return undefined;
	}
}

/**
 * Where a link in text an agent or a reviewer wrote may go. That text can repeat
 * whatever the agent read, so a link opens only a session's chat or a page on
 * the forges Tallpine reads PRs from; any other link is drawn as its words.
 */
export function safeLink(href: string): { kind: "session"; projectId: string; sessionId: string } | { kind: "web"; href: string; host: string } | undefined {
	const session = sessionLink(href);
	if (session) return { kind: "session", ...session };
	const web = forgeURL(href);
	return web ? { kind: "web", href: web, host: new URL(web).hostname } : undefined;
}

export type ReportBlock = { kind: "heading" | "paragraph" | "item" | "code"; text: string; marker?: string };

/** A report's blocks: the headings, list items and code fences workers write, and the paragraphs between. */
export function reportBlocks(text: string): ReportBlock[] {
	const blocks: ReportBlock[] = [];
	const lines = text.split("\n");
	let paragraph: string[] = [];
	const flush = () => {
		if (paragraph.length > 0) blocks.push({ kind: "paragraph", text: paragraph.join("\n") });
		paragraph = [];
	};
	for (let index = 0; index < lines.length; index += 1) {
		const line = lines[index]!;
		const fence = /^\s*(`{3,}|~{3,})/.exec(line);
		if (fence) {
			flush();
			const code: string[] = [];
			for (index += 1; index < lines.length && !lines[index]!.trimStart().startsWith(fence[1]!); index += 1) code.push(lines[index]!);
			blocks.push({ kind: "code", text: code.join("\n") });
			continue;
		}
		const heading = /^#{1,6}\s+(.+)$/.exec(line);
		const item = /^\s*([-*+]|\d+[.)])\s+(.*)$/.exec(line);
		if (heading) {
			flush();
			blocks.push({ kind: "heading", text: heading[1]!.trim() });
		} else if (item) {
			flush();
			blocks.push({ kind: "item", marker: /\d/.test(item[1]!) ? item[1]! : "•", text: item[2]! });
		} else if (line.trim() === "") {
			flush();
		} else {
			paragraph.push(line);
		}
	}
	flush();
	return blocks;
}

const CODE_SPAN = /`[^`\n]*`/g;
// Innermost first: `[![a](x)](y)` loses its image before its link is read.
const IMAGE = /!\[([^\[\]\n]*)\]\(([^()\s]*)\)/g;
// Links, autolinks, and the bare web URLs ChatMarkdown would otherwise link.
const LINK = /\[([^\]\n]+)\]\(([^)\s]+)\)|<((?:https?|ao):\/\/[^\s>]+)>|(https?:\/\/[^\s<]+)/g;

/** One stretch of Markdown outside code fences, made safe. */
function safeInline(text: string): string {
	// Code spans are set aside, not split on, so a link whose label holds code stays one link.
	const spans: string[] = [];
	let out = text.replace(/\u0000/g, "").replace(CODE_SPAN, (span) => `\u0000${spans.push(span) - 1}\u0000`);
	for (let previous = ""; previous !== out;) {
		previous = out;
		out = out.replace(IMAGE, (_image, alt: string) => alt);
	}
	out = out.replace(LINK, (match, label: string | undefined, href: string | undefined, auto: string | undefined, bare: string | undefined) => {
		if (label !== undefined && href !== undefined) {
			const target = safeLink(href);
			if (!target) return label;
			return target.kind === "web" && label !== href ? `${match} (${target.host})` : match;
		}
		const url = auto ?? bare!;
		if (safeLink(url.replace(/[.,;:!?'")\]]+$/, ""))) return match;
		return url.includes("`") ? `\`\` ${url} \`\`` : `\`${url}\``;
	});
	return out.replace(/\u0000(\d+)\u0000/g, (_token, index: string) => spans[Number(index)]!);
}

/**
 * Markdown an agent or a reviewer wrote, made safe to hand to `ChatMarkdown`, as
 * the desktop's `safeOrigin` does: a link stays a link only where `safeLink`
 * allows, and then names its host when its label is something else; any other
 * link keeps its words, a bare URL becomes code so it cannot be linked, and an
 * image, even one inside a link, becomes its alt text so nothing loads from
 * where the text points. Code is left as written.
 */
export function safeMarkdown(text: string): string {
	// A fence runs to its closing fence, or to the end of the text when it has none.
	return text.split(/(^ {0,3}(?:`{3,}|~{3,})[^\n]*\n[\s\S]*?(?:\n {0,3}(?:`{3,}|~{3,})[ \t]*(?=\n|(?![\s\S]))|(?![\s\S])))/m)
		.map((part, index) => index % 2 === 1 ? part : safeInline(part))
		.join("");
}
