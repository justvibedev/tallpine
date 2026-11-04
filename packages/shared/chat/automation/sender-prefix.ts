// The text prefixes Tallpine and outside tools put in front of a message's body.
// Writers: `tallpine send` ([from ...]) and integrations such as grok-bot ([via ...]).
// Desktop and phone read them through here.

/** The `[from <session> "<name>"]` prefix `tallpine send` puts on a worker's message. */
export const SENDER_PREFIX = /^\[from (?:worker\s+)?([\w.-]+)(?:\s+"([^"]*)")?\]/;

/**
 * The `[via <tool>]` or `[via <tool> #<tag>]` prefix an outside tool puts on what it sends
 * through Tallpine's API. The tag only lets the tool find its reply.
 */
export const VIA_PREFIX = /^\[via ([^\]#\n]{1,60}?)(?: #[^\]\n]*)?\]\s*/;

export function stripSenderPrefix(text: string, senderSessionId: string): string {
	const escaped = senderSessionId.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	return text.replace(new RegExp(`^\\[from (?:worker\\s+)?${escaped}(?:\\s+"[^"]*")?\\]\\s*`), "");
}

/** The outside tool a message came through and what it said, when it carries a `[via ...]` prefix. */
export function parseViaPrefix(text: string): { name: string; body: string } | null {
	const match = VIA_PREFIX.exec(text);
	if (!match?.[1]) return null;
	return { name: match[1].trim(), body: text.slice(match[0].length) };
}
