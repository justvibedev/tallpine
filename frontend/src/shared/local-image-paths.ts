/**
 * Local image paths in an agent reply, for the thumbnail strip under it.
 *
 * Agents often answer "screenshot saved to `/…/artifacts/<session>/shot.png`"
 * instead of rendering it. This finds such paths (inline code, a markdown link,
 * or bare prose). Pure and DOM-free so mobile chat can reuse it.
 *
 * Finding a path grants nothing: main decides which confined route, if any,
 * serves it (`LocalPathInspection.image`), against the session's real roots.
 */

import { findLocalPaths, isLocalPathCandidate } from "./local-path";

export const LOCAL_IMAGE_EXTENSION = /\.(?:png|jpe?g|gif|webp)$/i;
/** Enough for a reply that lists a screenshot set; more is a file listing, not a gallery. */
export const MAX_LOCAL_IMAGE_PATHS = 12;

const FENCE = /^(?: {0,3})(`{3,}|~{3,})[^\n]*\n[\s\S]*?(?:^ {0,3}\1[^\n]*$|(?![\s\S]))/gm;
const INLINE_CODE = /(`+)([^`\n]+?)\1(?!`)/g;
// `[label](/path)` and `[label](</path with spaces.png>)`; the href may be percent-encoded or a file:// URL.
const LINK_HREF = /\]\(\s*(?:<([^>\n]+)>|([^)\s]+))/g;

function decodeHref(href: string): string | undefined {
	let value = href;
	if (/^file:\/\//i.test(value)) value = value.slice("file://".length);
	try {
		return decodeURI(value);
	} catch {
		return undefined;
	}
}

/** Video and audio containers a browser can play; `fileMediaKind` in the renderer classifies by these too. */
export const LOCAL_VIDEO_EXTENSION = /\.(?:m4v|mov|mp4|ogv|webm)$/i;
export const LOCAL_AUDIO_EXTENSION = /\.(?:aac|flac|m4a|mp3|oga|ogg|opus|wav)$/i;
/** Players are heavy; a reply naming more than this is a file listing. */
export const MAX_LOCAL_MEDIA_PATHS = 4;

function isConfinableLocalPath(path: string, extension: RegExp): boolean {
	return isLocalPathCandidate(path) && path.startsWith("/") && extension.test(path) && !path.split("/").includes("..");
}

/** An absolute image path a thumbnail may be shown for: no `~`, no `..`, an image extension. */
export function isLocalImagePath(path: string): boolean {
	return isConfinableLocalPath(path, LOCAL_IMAGE_EXTENSION);
}

/** An absolute video or audio path an inline player may be shown for, under the same rules as an image. */
export function isLocalMediaPath(path: string): boolean {
	return isConfinableLocalPath(path, LOCAL_VIDEO_EXTENSION) || isConfinableLocalPath(path, LOCAL_AUDIO_EXTENSION);
}

/** Absolute local image paths in a markdown reply, in order of first mention, without duplicates. Fenced code is skipped. */
export function findLocalImagePaths(markdown: string, max = MAX_LOCAL_IMAGE_PATHS): string[] {
	return findLocalPathsWhere(markdown, isLocalImagePath, max);
}

/** Absolute local video and audio paths in a markdown reply, found the same way as images. */
export function findLocalMediaPaths(markdown: string, max = MAX_LOCAL_MEDIA_PATHS): string[] {
	return findLocalPathsWhere(markdown, isLocalMediaPath, max);
}

function findLocalPathsWhere(markdown: string, accept: (path: string) => boolean, max: number): string[] {
	const prose = markdown.replace(FENCE, "");
	const found: { index: number; path: string }[] = [];
	// An inline code span is one token, so a name with lowercase words after a space still counts whole.
	for (const match of prose.matchAll(INLINE_CODE)) found.push({ index: match.index, path: match[2]!.trim() });
	// A link href reappears in the prose scan percent-encoded; decoding makes the two one path.
	for (const match of findLocalPaths(prose)) {
		const path = decodeHref(match.path);
		if (path) found.push({ index: match.start, path });
	}
	for (const match of prose.matchAll(LINK_HREF)) {
		const path = decodeHref(match[1] ?? match[2] ?? "");
		if (path) found.push({ index: match.index, path });
	}
	found.sort((left, right) => left.index - right.index);
	const paths: string[] = [];
	for (const { path } of found) {
		if (paths.length >= max) break;
		if (accept(path) && !paths.includes(path)) paths.push(path);
	}
	return paths;
}
