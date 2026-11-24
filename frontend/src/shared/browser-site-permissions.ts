/**
 * Site permissions in the Browser panel (B855). A page in a person-owned tab
 * that asks for one of these kinds gets an Allow / Block bar; the answer is
 * remembered per origin and profile (main/browser-site-permission-store.ts).
 * Agent-driven and proxied tabs keep denying everything.
 */
export const SITE_PERMISSION_KINDS = ["camera", "microphone", "notifications", "clipboard-read"] as const;

export type SitePermissionKind = (typeof SITE_PERMISSION_KINDS)[number];

export type SitePermissionDecision = "allow" | "block";

export type BrowserSitePermissionEntry = { kind: SitePermissionKind; decision: SitePermissionDecision };

/**
 * What the bar asks about. A permission prompt names `kinds`; a link the page
 * wants to hand to another app (mailto:, tel:) names `externalUrl` instead and
 * is a one-time confirm that is never remembered.
 */
export type BrowserSitePermissionPrompt = {
	id: string;
	origin: string;
	kinds: SitePermissionKind[];
	externalUrl?: string;
};

export type BrowserSitePermissionRespondInput = { viewId: string; promptId: string; allow: boolean };

/** Forgets one remembered kind for the active tab's origin, or all of them. */
export type BrowserSitePermissionResetInput = { viewId: string; kind?: SitePermissionKind };

// Granted without a prompt in a person-owned tab, as Chrome does: the page
// needs a click to ask, and fullscreen stays inside the panel's view.
const PERSON_TAB_AUTO_ALLOWED: ReadonlySet<string> = new Set(["fullscreen", "pointerLock"]);

export function isAutoAllowedSitePermission(permission: string): boolean {
	return PERSON_TAB_AUTO_ALLOWED.has(permission);
}

export function isSitePermissionKind(value: unknown): value is SitePermissionKind {
	return typeof value === "string" && (SITE_PERMISSION_KINDS as readonly string[]).includes(value);
}

function mediaKind(type: string | undefined): SitePermissionKind | undefined {
	if (type === "video") return "camera";
	if (type === "audio") return "microphone";
	return undefined;
}

/**
 * The kinds an Electron permission request asks for, or undefined when the
 * request is not one the bar offers (it stays denied). `media` carries the
 * device types in `mediaTypes` (request) or `mediaType` (check).
 */
export function sitePermissionKinds(
	permission: string,
	details: { mediaTypes?: readonly string[]; mediaType?: string } = {},
): SitePermissionKind[] | undefined {
	if (permission === "media") {
		const types = details.mediaTypes ?? (details.mediaType ? [details.mediaType] : []);
		const kinds = [...new Set(types.map(mediaKind))];
		if (!kinds.length || kinds.some((kind) => kind === undefined)) return undefined;
		return kinds as SitePermissionKind[];
	}
	return isSitePermissionKind(permission) ? [permission] : undefined;
}

/** The http(s) origin a decision is remembered under, if the URL has one. */
export function sitePermissionOrigin(url: string | undefined): string | undefined {
	if (!url) return undefined;
	try {
		const parsed = new URL(url);
		return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.origin : undefined;
	} catch {
		return undefined;
	}
}

const EXTERNAL_PROTOCOLS: ReadonlySet<string> = new Set(["mailto:", "tel:"]);

/** Links a page may hand to the system (mail app, phone) after a confirm. */
export function isConfirmableExternalUrl(url: string | undefined): boolean {
	if (!url) return false;
	try {
		return EXTERNAL_PROTOCOLS.has(new URL(url).protocol);
	} catch {
		return false;
	}
}
