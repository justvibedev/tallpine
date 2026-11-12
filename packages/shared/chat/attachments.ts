// These are Tallpine-owned prompt suffixes, not general markdown. Chat and spawn used
// slightly different wording, and older conversations used "Attached images";
// accepting every shipped form lets the transcript improve without rewriting
// its durable history. Earlier mobile builds wrote "Attached files are available in the worktree".
// Desktop and phone both read through this module; current image messages use the
// placeholder block in ./image-placeholders.
import { IMAGE_ATTACHMENT_PATH, STAGED_ATTACHMENT_PATH, placeholderParts, withOriginals, withPlaceholderBlock, type StagedParts } from "./image-placeholders";

export { IMAGE_ATTACHMENT_PATH } from "./image-placeholders";
export type { StagedParts } from "./image-placeholders";

// Earlier desktop builds followed the reference block with a list of downscaled
// images and their originals; it is Tallpine's text too, so it is read back out.
const ATTACHMENT_REFERENCE_BLOCK =
	/(?:^|\n\n)(?:Attached files \(read these files in the workspace(?: for context)?\)|Attached images \(read these files in the workspace for visual context\)|Attached files are available in the worktree):\n((?:- [^\n]+(?:\n|$))+?)(?:\nDownscaled images \(open the original only if the detail matters\):\n((?:- [^\n]+(?:\n|$))+))?$/;
const DOWNSCALED_LINE = /^- (\S+) was [^\n]+; full original: (\S+)$/;

export function stagedAttachmentParts(text: string): StagedParts {
	const current = placeholderParts(text);
	if (current) return current;
	const unchanged = { body: text, attachments: [] };
	const match = ATTACHMENT_REFERENCE_BLOCK.exec(text);
	if (!match?.[1]) return unchanged;

	const attachments = match[1]
		.trimEnd()
		.split("\n")
		.map((line) => line.slice(2));
	// Only reinterpret paths Tallpine itself stages. A user can write an identically
	// worded example about docs/screenshot.png; that prose must remain untouched.
	if (attachments.length === 0 || attachments.some((path) => !STAGED_ATTACHMENT_PATH.test(path))) {
		return unchanged;
	}
	const originals: Record<string, string> = {};
	for (const line of match[2]?.trimEnd().split("\n") ?? []) {
		const downscaled = DOWNSCALED_LINE.exec(line);
		if (!downscaled?.[1] || !downscaled[2] || !attachments.includes(downscaled[1]) || !STAGED_ATTACHMENT_PATH.test(downscaled[2])) {
			return unchanged;
		}
		originals[downscaled[1]] = downscaled[2];
	}
	// The match begins at the generated separator, so slicing at its index
	// removes only Tallpine-owned text and preserves the authored body byte-for-byte.
	return withOriginals({ body: text.slice(0, match.index), attachments }, originals);
}

export function attachmentName(path: string): string {
	return path.slice(path.lastIndexOf("/") + 1);
}

export function attachmentURL(apiBaseUrl: string, sessionId: string, path: string): string {
	const route = `/api/v1/sessions/${encodeURIComponent(sessionId)}/preview/files/${path
		.split("/")
		.map(encodeURIComponent)
		.join("/")}`;
	return apiBaseUrl ? `${apiBaseUrl.replace(/\/+$/, "")}${route}` : route;
}

// A staged image path written into the prose itself: the composer's inline image
// chip serializes to exactly this, so the agent can tell which image a sentence
// is about.
// The lookbehind keeps a longer path such as `/wt/.tallpine/attachments/...` (or the legacy `.ao/attachments`) whole.
const INLINE_IMAGE_PATH = /(?<!\/)\.(?:tallpine|ao)\/attachments\/(?:attachment|image)-[A-Za-z0-9][A-Za-z0-9._-]*\.(?:png|jpe?g|gif|webp|bmp)(?![A-Za-z0-9_-])/gi;

/** Whether a staged path is one the inline-image renderers can read back out of prose. */
export function isInlineImagePath(path: string): boolean {
	const segments = splitInlineImagePaths(path);
	return segments.length === 1 && segments[0]?.path === path;
}

export type InlineImageSegment = { text: string; path?: undefined } | { path: string; text?: undefined };

/** Split prose around inline staged image paths, optionally only those `keep` accepts. */
export function splitInlineImagePaths(text: string, keep: (path: string) => boolean = () => true): InlineImageSegment[] {
	const segments: InlineImageSegment[] = [];
	let last = 0;
	for (const match of text.matchAll(INLINE_IMAGE_PATH)) {
		if (!keep(match[0])) continue;
		if (match.index > last) segments.push({ text: text.slice(last, match.index) });
		segments.push({ path: match[0] });
		last = match.index + match[0].length;
	}
	if (last < text.length) segments.push({ text: text.slice(last) });
	return segments;
}

/**
 * Text with one inline image taken out, as removing its chip would: the path goes
 * with one following space, so the words around it don't double-space.
 */
export function removeInlineImagePath(text: string, path: string): string {
	return text
		.split("\n")
		.map((line) => {
			let kept = "";
			let afterImage = false;
			for (const segment of splitInlineImagePaths(line, (candidate) => candidate === path)) {
				if (segment.path !== undefined) afterImage = true;
				else {
					kept += afterImage && segment.text.startsWith(" ") ? segment.text.slice(1) : segment.text;
					afterImage = false;
				}
			}
			return kept;
		})
		.join("\n");
}

/**
 * A message that is nothing but its attached images keeps no prose, since the
 * reference block already names them. Anything else is sent exactly as written:
 * a path the user typed is their text, and only the composer can tell a stale
 * chip from it (see the editor's pruneImages).
 */
export function proseBesideImages(text: string, attached: string[]): string {
	const onlyImages = splitInlineImagePaths(text).every((segment) =>
		segment.path === undefined ? segment.text.trim() === "" : attached.includes(segment.path));
	return onlyImages ? "" : text;
}

/**
 * Plain-text form for one-line surfaces: only the owner's words, never Tallpine's
 * attachment block. Legacy inline image paths read as `[Image N]`; a message that
 * is only attachments reads as its image labels and file names.
 */
export function labelInlineImages(text: string, label: (index: number) => string = (index) => `Image ${index}`): string {
	const { body, attachments } = stagedAttachmentParts(text);
	if (attachments.length === 0) return text;
	const images = attachments.filter((path) => IMAGE_ATTACHMENT_PATH.test(path));
	const labelled = splitInlineImagePaths(body, (path) => images.includes(path))
		.map((segment) => segment.path === undefined ? segment.text : `[${label(images.indexOf(segment.path) + 1)}]`)
		.join("")
		.trim();
	return labelled || attachments
		.map((path) => images.includes(path) ? `[${label(images.indexOf(path) + 1)}]` : attachmentName(path))
		.join(" ");
}

/**
 * Appends staged paths using the chat wording, so either surface renders them. A
 * message with images gets the placeholder block, numbering images in path order
 * and naming each downscaled image's full original from `originals`.
 */
export function withAttachmentReferences(text: string, paths: string[], originals: Record<string, string> = {}): string {
	if (paths.length === 0) return text;
	if (paths.some((path) => IMAGE_ATTACHMENT_PATH.test(path))) return withPlaceholderBlock(text, paths, originals);
	const body = text.trim();
	return `${body}${body ? "\n\n" : ""}Attached files (read these files in the workspace):\n${paths.map((path) => `- ${path}`).join("\n")}`;
}

export function withEditedBody(body: string, attachments: string[], originals: Record<string, string> = {}): string {
	return attachments.length ? withAttachmentReferences(body, attachments, originals) : body.trimEnd();
}

export function isImageAttachment(path: string): boolean {
	return IMAGE_ATTACHMENT_PATH.test(path);
}
