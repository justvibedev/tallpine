// Plain-text previews of chat text, for one-line surfaces on desktop and phone.
import { stagedAttachmentParts } from "./attachments";

/** Markdown flattened to one line, capped at `limit` characters. */
export function previewText(value: string, limit: number): string {
	const plain = value
		.replace(/```[\s\S]*?```/g, " code sample ")
		.replace(/\[([^\]]+)]\([^)]*\)/g, "$1")
		.replace(/[*_`#>~]+/g, " ")
		.replace(/\s+/g, " ")
		.trim();
	return plain.length > limit ? `${plain.slice(0, limit - 1).trimEnd()}…` : plain;
}

/** A human prompt's one-line title: its text, or a count of its attachments when it is only those. */
export function humanMessageTitle(text: string): string {
	const { body, attachments } = stagedAttachmentParts(text);
	if (attachments.length === 0) return text;
	return body.trim() || (attachments.length === 1 ? "1 attachment" : `${attachments.length} attachments`);
}
