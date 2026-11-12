// Chat images travel as short placeholders. The owner's text carries `[IMG1]`,
// `[IMG2]`... where each image belongs, and a compact Tallpine-owned block after a
// `---` separator maps every placeholder to its staged (downscaled) path and full
// original. The bubble shows only the text and thumbnails; the agent reads both.
// Desktop and phone both build and read this format here.

export const STAGED_ATTACHMENT_PATH = /^\.(?:tallpine|ao)\/attachments\/(?:attachment|image)-[A-Za-z0-9][A-Za-z0-9._-]*$/;
export const IMAGE_ATTACHMENT_PATH = /\.(?:png|jpe?g|gif|webp|bmp)$/i;

/** The one-line instruction heading the block; the parser matches it exactly. */
export const PLACEHOLDER_BLOCK_HEADER =
	"Attached files (read these files in the workspace; [IMGn] marks where each image belongs in the message, and a full original is worth opening only if the detail matters):";

const PLACEHOLDER = /\[IMG([1-9]\d*)\]/g;
const PLACEHOLDER_BLOCK = new RegExp(
	`(?:^|\\n\\n)---\\n${PLACEHOLDER_BLOCK_HEADER.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\n((?:- [^\\n]+(?:\\n|$))+)$`,
);
const PLACEHOLDER_LINE = /^- (?:\[IMG([1-9]\d*)\] )?(\S+)(?: \(full original: (\S+)\))?$/;

export function imagePlaceholder(index: number): string {
	return `[IMG${index}]`;
}

/** `originals` maps a downscaled image's staged path to its full original; absent when none was downscaled. */
export type StagedParts = { body: string; attachments: string[]; originals?: Record<string, string> };

export function withOriginals(parts: { body: string; attachments: string[] }, originals: Record<string, string>): StagedParts {
	return Object.keys(originals).length ? { ...parts, originals } : parts;
}

/** The staged paths, with each image numbered in order and its full original beside it. */
export function withPlaceholderBlock(text: string, paths: string[], originals: Record<string, string> = {}): string {
	let image = 0;
	const lines = paths.map((path) => {
		if (!IMAGE_ATTACHMENT_PATH.test(path)) return `- ${path}`;
		image += 1;
		const original = originals[path];
		return `- ${imagePlaceholder(image)} ${path}${original ? ` (full original: ${original})` : ""}`;
	});
	const body = text.trim();
	return `${body}${body ? "\n\n" : ""}---\n${PLACEHOLDER_BLOCK_HEADER}\n${lines.join("\n")}`;
}

/** Splits a placeholder-format message; undefined when the text has no such block. */
export function placeholderParts(text: string): StagedParts | undefined {
	const match = PLACEHOLDER_BLOCK.exec(text);
	if (!match?.[1]) return undefined;
	const attachments: string[] = [];
	const originals: Record<string, string> = {};
	let image = 0;
	for (const line of match[1].trimEnd().split("\n")) {
		const parsed = PLACEHOLDER_LINE.exec(line);
		const path = parsed?.[2];
		// Only paths Tallpine stages, numbered as the builder numbers them: anything
		// else is the user's own prose and stays on screen untouched.
		if (!parsed || !path || !STAGED_ATTACHMENT_PATH.test(path)) return undefined;
		const isImage = IMAGE_ATTACHMENT_PATH.test(path);
		if (isImage) image += 1;
		if ((parsed[1] !== undefined) !== isImage || (isImage && Number(parsed[1]) !== image)) return undefined;
		if (parsed[3]) {
			if (!isImage || !STAGED_ATTACHMENT_PATH.test(parsed[3])) return undefined;
			originals[path] = parsed[3];
		}
		attachments.push(path);
	}
	return withOriginals({ body: text.slice(0, match.index), attachments }, originals);
}

/** Composer text with each attached image's inline path replaced by its placeholder. */
export function placeholdersForPaths(text: string, images: string[], split: (text: string) => Array<{ text?: string; path?: string }>): string {
	return split(text)
		.map((segment) => {
			if (segment.path === undefined) return segment.text ?? "";
			const index = images.indexOf(segment.path);
			return index < 0 ? segment.path : imagePlaceholder(index + 1);
		})
		.join("");
}

/** The inverse, for restoring a sent body into a composer that draws paths as chips. */
export function pathsForPlaceholders(text: string, images: string[]): string {
	return text.replace(PLACEHOLDER, (whole, index: string) => images[Number(index) - 1] ?? whole);
}

/** Text with `[IMGn]` gone (with one following space) and later placeholders renumbered down. */
export function removeImagePlaceholder(text: string, index: number): string {
	return text
		.replace(new RegExp(`\\[IMG${index}\\] ?`, "g"), "")
		.replace(PLACEHOLDER, (whole, n: string) => Number(n) > index ? imagePlaceholder(Number(n) - 1) : whole);
}

/**
 * Inserts `[IMGn]` for each new image at the selection, padded with spaces so it
 * never runs into the words around it. Returns the text and the caret after it.
 */
export function insertImagePlaceholders(
	text: string,
	selection: { start: number; end: number },
	first: number,
	count: number,
): { text: string; caret: number } {
	const start = Math.max(0, Math.min(selection.start, text.length));
	const end = Math.max(start, Math.min(selection.end, text.length));
	const before = text.slice(0, start);
	const after = text.slice(end);
	const tokens = Array.from({ length: count }, (_, offset) => imagePlaceholder(first + offset)).join(" ");
	const lead = before && !/\s$/.test(before) ? " " : "";
	const inserted = `${lead}${tokens}${after.startsWith(" ") ? "" : " "}`;
	return { text: before + inserted + after, caret: before.length + inserted.length };
}
