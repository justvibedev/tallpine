import { IN_APP_LINK_SCHEME, LEGACY_IN_APP_LINK_SCHEME } from "./brand";

/** Strict parsing of in-app `tallpine://sessions/...` links; shared by the renderer and the main process. */
export type SessionLinkTarget = { projectId: string; sessionId: string };
export type ArtifactLinkTarget = SessionLinkTarget & { path: string };

/** Both schemes are parsed: stored messages keep the legacy one. */
const SESSION_LINK_PREFIXES = [`${IN_APP_LINK_SCHEME}://sessions/`, `${LEGACY_IN_APP_LINK_SCHEME}://sessions/`] as const;

/** True when the value uses an in-app link scheme (`tallpine://` or the legacy scheme), whether or not it is a known route. */
export function hasInAppLinkScheme(value: string): boolean {
	return value.startsWith(`${IN_APP_LINK_SCHEME}://`) || value.startsWith(`${LEGACY_IN_APP_LINK_SCHEME}://`);
}
const ARTIFACT_SEGMENT = "artifacts";

/** Strictly parse the in-app session route, `tallpine://sessions/{project}/{session}`. */
export function parseSessionLink(value: string): SessionLinkTarget | undefined {
	const segments = linkSegments(value);
	if (segments?.length !== 2) return undefined;
	return sessionTarget(segments);
}

/** Strictly parse the in-app artifact route, `tallpine://sessions/{project}/{session}/artifacts/{path}`. */
export function parseArtifactLink(value: string): ArtifactLinkTarget | undefined {
	const segments = linkSegments(value);
	if (!segments || segments.length < 4 || segments[2] !== ARTIFACT_SEGMENT) return undefined;
	const target = sessionTarget(segments.slice(0, 2));
	if (!target) return undefined;
	const path: string[] = [];
	for (const segment of segments.slice(3)) {
		const decoded = decodeSegment(segment);
		if (!decoded?.trim() || decoded === "." || decoded === ".." || /[/\\]/.test(decoded)) return undefined;
		path.push(decoded);
	}
	return { ...target, path: path.join("/") };
}

function linkSegments(value: string): string[] | undefined {
	const prefix = SESSION_LINK_PREFIXES.find((candidate) => value.startsWith(candidate));
	if (!prefix || value.includes("?") || value.includes("#")) return undefined;
	const segments = value.slice(prefix.length).split("/");
	return segments.some((segment) => segment.length === 0) ? undefined : segments;
}

function sessionTarget([project, session]: string[]): SessionLinkTarget | undefined {
	const projectId = decodeSegment(project!);
	const sessionId = decodeSegment(session!);
	if (!projectId?.trim() || !sessionId?.trim() || projectId.includes("/") || sessionId.includes("/")) return undefined;
	return { projectId, sessionId };
}

function decodeSegment(segment: string): string | undefined {
	try {
		return decodeURIComponent(segment);
	} catch {
		return undefined;
	}
}

