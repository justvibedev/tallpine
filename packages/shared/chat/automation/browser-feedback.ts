// Reads the `<browser_annotations>` handoff the desktop browser writes
// (frontend/src/shared/browser-annotations.ts formatBrowserAnnotationMessage), so the
// transcript can draw a concise card instead of selectors and agent-facing instructions.
// Writer and reader are tested together there; desktop and phone share this reader.

export type BrowserAnnotationKind = "comment" | "adjustment";

export type ParsedBrowserAnnotationItem = {
	number: number;
	kind: BrowserAnnotationKind;
	target: string;
	text: string;
	comment: string;
	changes: string[];
};

export type ParsedBrowserAnnotationMessage = {
	pageTitle: string;
	pageUrl: string;
	items: ParsedBrowserAnnotationItem[];
	screenshotCount: number;
};

const BROWSER_ANNOTATIONS_BLOCK_PATTERN = /^<browser_annotations>\n([\s\S]*?)\n<\/browser_annotations>$/;

/**
 * Parse Tallpine's annotation transport so the transcript can render a concise card
 * instead of exposing selectors and agent-facing handoff instructions.
 */
export function parseBrowserAnnotationMessage(message: string): ParsedBrowserAnnotationMessage | null {
	const match = BROWSER_ANNOTATIONS_BLOCK_PATTERN.exec(message.trim());
	if (!match) return null;

	const lines = (match[1] ?? "").split("\n");
	const pageTitle = valueAfterPrefix(lines, "Page: ") || "Untitled page";
	const pageUrl = valueAfterPrefix(lines, "URL: ");
	const items: ParsedBrowserAnnotationItem[] = [];
	let current: ParsedBrowserAnnotationItem | null = null;
	let readingChanges = false;
	let readingScreenshots = false;
	let screenshotCount = 0;

	for (const line of lines) {
		const heading = /^Annotation (\d+) \((comment|adjustment)\):$/.exec(line);
		if (heading) {
			current = {
				number: Number(heading[1]),
				kind: heading[2] as BrowserAnnotationKind,
				target: "",
				text: "",
				comment: "",
				changes: [],
			};
			items.push(current);
			readingChanges = false;
			readingScreenshots = false;
			continue;
		}
		if (line === "Requested visual changes:") {
			readingChanges = true;
			continue;
		}
		if (line === "Reference screenshots:") {
			current = null;
			readingChanges = false;
			readingScreenshots = true;
			continue;
		}
		if (readingScreenshots && line.startsWith("- ")) {
			screenshotCount += 1;
			continue;
		}
		if (!current) continue;
		if (line.startsWith("Target: ")) current.target = line.slice("Target: ".length).trim();
		else if (line.startsWith("Element text: ")) current.text = quotedField(line.slice("Element text: ".length).trim());
		else if (line.startsWith("Comment: ")) current.comment = line.slice("Comment: ".length).trim();
		else if (readingChanges && line.startsWith("- ")) current.changes.push(line.slice(2).trim());
	}

	return { pageTitle, pageUrl, items, screenshotCount };
}

function valueAfterPrefix(lines: string[], prefix: string): string {
	return lines.find((line) => line.startsWith(prefix))?.slice(prefix.length).trim() ?? "";
}

/** A field the writer JSON-quoted, unquoted; anything else is returned as written. */
export function quotedField(value: string): string {
	if (!value.startsWith('"')) return value;
	try {
		const parsed = JSON.parse(value);
		return typeof parsed === "string" ? parsed : value;
	} catch {
		return value;
	}
}
