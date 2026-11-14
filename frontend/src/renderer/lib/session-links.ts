import { IN_APP_LINK_SCHEME, LEGACY_IN_APP_LINK_SCHEME } from "../../shared/brand";
import { parseArtifactLink, parseSessionLink, type ArtifactLinkTarget, type SessionLinkTarget } from "../../shared/session-link-parse";
export { parseArtifactLink, parseSessionLink };
export type { ArtifactLinkTarget, SessionLinkTarget };
export type ResolvedSessionLink = SessionLinkTarget & { isTerminated: boolean };
export type SessionLinkWorkspace = {
	id: string;
	sessions: Array<{ id: string; isTerminated?: boolean; status?: string }>;
};

const SESSION_LINK_PREFIX = "tallpine://sessions/";
const ARTIFACT_SEGMENT = "artifacts";
const RAW_SESSION_LINK = new RegExp(`(?:${LEGACY_IN_APP_LINK_SCHEME}|${IN_APP_LINK_SCHEME}):\\/\\/sessions\\/\\S+`, "g");
const TRAILING_PUNCTUATION = /[.,;:!?]+$/;
const TRAILING_DELIMITERS = [
	["(", ")"],
	["[", "]"],
	["{", "}"],
	["<", ">"],
	["\"", "\""],
	["'", "'"],
	["`", "`"],
	["“", "”"],
	["‘", "’"],
	["«", "»"],
	["‹", "›"],
] as const;

/** The canonical in-app route for a session. */
export function sessionLink(projectId: string, sessionId: string): string {
	return `${SESSION_LINK_PREFIX}${encodeURIComponent(projectId)}/${encodeURIComponent(sessionId)}`;
}

/** The canonical artifact route for a slash-separated path inside a session's artifact directory. */
export function artifactLink(projectId: string, sessionId: string, path: string): string {
	const encodedPath = path.split("/").map(encodeURIComponent).join("/");
	return `${SESSION_LINK_PREFIX}${encodeURIComponent(projectId)}/${encodeURIComponent(sessionId)}/${ARTIFACT_SEGMENT}/${encodedPath}`;
}

type ArtifactPathWorkspace = { id: string; sessions: Array<{ id: string; artifactFiles?: Array<{ path: string }> }> };

/**
 * The artifact route for text naming a listed session artifact, either as
 * `{session}/{path}` or as a path containing `/artifacts/{project}/{session}/{path}`
 * (or the older flat `/artifacts/{session}/{path}`, still used by live sessions).
 */
export function artifactLinkForPath(value: string, workspaces: ArtifactPathWorkspace[]): string | undefined {
	const normalized = value.trim().replace(/\\/g, "/");
	const candidates = normalized.startsWith("/") || /^[A-Za-z]:\//.test(normalized) ? [] : [normalized];
	const marker = `/${ARTIFACT_SEGMENT}/`;
	for (let index = normalized.indexOf(marker); index >= 0; index = normalized.indexOf(marker, index + 1)) {
		const tail = normalized.slice(index + marker.length);
		candidates.push(tail);
		const projectSlash = tail.indexOf("/");
		if (projectSlash > 0) candidates.push(tail.slice(projectSlash + 1));
	}
	for (const candidate of candidates) {
		const slash = candidate.indexOf("/");
		if (slash <= 0) continue;
		const sessionId = candidate.slice(0, slash);
		const path = candidate.slice(slash + 1);
		for (const workspace of workspaces) {
			const session = workspace.sessions.find((entry) => entry.id === sessionId);
			if (session?.artifactFiles?.some((file) => file.path === path)) return artifactLink(workspace.id, sessionId, path);
		}
	}
	return undefined;
}

/** True for either in-app route: a session or one of its artifacts. */
export function isTallpineLink(value: string): boolean {
	return parseSessionLink(value) !== undefined || parseArtifactLink(value) !== undefined;
}

export function isSessionLink(value: string): boolean {
	return parseSessionLink(value) !== undefined;
}

export function resolveSessionLink(value: string, workspaces: SessionLinkWorkspace[]): ResolvedSessionLink | undefined {
	const target = parseSessionLink(value) ?? parseArtifactLink(value);
	if (!target) return undefined;
	const project = workspaces.find((workspace) => workspace.id === target.projectId);
	const session = project?.sessions.find((candidate) => candidate.id === target.sessionId);
	if (!session) return undefined;
	return { projectId: target.projectId, sessionId: target.sessionId, isTerminated: session.isTerminated === true || session.status === "terminated" };
}

/** Find raw session and artifact links without swallowing prose punctuation adjacent to them. */
export function findSessionLinks(text: string): Array<{ start: number; end: number; text: string }> {
	const matches: Array<{ start: number; end: number; text: string }> = [];
	for (const match of text.matchAll(RAW_SESSION_LINK)) {
		const candidate = trimSessionLinkCandidate(match[0], text.slice(0, match.index));
		if (!isTallpineLink(candidate)) continue;
		matches.push({ start: match.index, end: match.index + candidate.length, text: candidate });
	}
	return matches;
}

function trimSessionLinkCandidate(value: string, precedingText: string): string {
	let candidate = value;
	let previous = "";
	while (candidate !== previous) {
		previous = candidate;
		if (precedingText.endsWith("&lt;") && candidate.endsWith("&gt;")) {
			candidate = candidate.slice(0, -"&gt;".length);
		}
		candidate = candidate.replace(TRAILING_PUNCTUATION, "");
		for (const [opening, closing] of TRAILING_DELIMITERS) {
			if (!candidate.endsWith(closing)) continue;
			const wrapsLink = precedingText.endsWith(opening);
			const unbalanced = opening !== closing && count(candidate, opening) < count(candidate, closing);
			if (wrapsLink || unbalanced) candidate = candidate.slice(0, -closing.length);
		}
	}
	return candidate;
}

function count(value: string, character: string): number {
	return [...value].filter((entry) => entry === character).length;
}

type MarkdownNode = { type: string; value?: string; url?: string; children?: MarkdownNode[] };

/** remark plugin that linkifies only raw canonical session and artifact URLs outside code. */
export function remarkSessionLinks() {
	return (tree: MarkdownNode) => visitMarkdown(tree);
}

function visitMarkdown(node: MarkdownNode): void {
	if (!node.children || node.type === "code" || node.type === "inlineCode" || node.type === "link") return;
	for (let index = 0; index < node.children.length; index += 1) {
		const child = node.children[index]!;
		if (child.type !== "text" || !child.value) {
			visitMarkdown(child);
			continue;
		}
		const links = findSessionLinks(child.value);
		if (links.length === 0) continue;
		const replacement: MarkdownNode[] = [];
		let cursor = 0;
		for (const link of links) {
			if (link.start > cursor) replacement.push({ type: "text", value: child.value.slice(cursor, link.start) });
			replacement.push({ type: "link", url: link.text, children: [{ type: "text", value: link.text }] });
			cursor = link.end;
		}
		if (cursor < child.value.length) replacement.push({ type: "text", value: child.value.slice(cursor) });
		node.children.splice(index, 1, ...replacement);
		index += replacement.length - 1;
	}
}
