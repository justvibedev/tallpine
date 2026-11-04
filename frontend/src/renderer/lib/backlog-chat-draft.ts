import type { BacklogItem } from "./backlog-api";
import { activateChatDraftScope, loadChatSessionDraft, writeChatComposerText } from "./chat-drafts";

/** What the orchestrator's chat needs to know to prefill a draft in the right place. */
export type DraftTarget = { id: string; createdAt?: string };

/** The whole item as plain text for a conversation. Nothing here asks the agent to build anything. */
export function discussText(item: BacklogItem): string {
	const lines = [`Let's talk through backlog item ${item.ref} before anything is built.`, "", `${item.ref} · ${item.title} (${item.state}${item.hold ? `, held: ${item.hold}` : ""})`];
	if (item.note) lines.push("", "Note:", item.note);
	if (item.doneWhen) lines.push("", `Done when: ${item.doneWhen}`);
	if (item.scope) lines.push("", `Scope: ${item.scope}`);
	if (item.parentRef) lines.push("", `Part of ${item.parentRef}`);
	const links = item.links ?? [];
	if (links.length > 0) lines.push("", "Links:", ...links.map((link) => `- ${link.kind}: ${link.display}`));
	if (item.agentNotes) lines.push("", "Agent notes:", item.agentNotes);
	const fields = Object.entries(item.fields ?? {});
	if (fields.length > 0) lines.push("", "Fields:", ...fields.map(([key, value]) => `- ${key}: ${value}`));
	if (item.origin.excerpt) lines.push("", `Source (${item.origin.kind}${item.origin.sessionId ? ` ${item.origin.sessionId}` : ""}): ${item.origin.excerpt}`);
	return lines.join("\n");
}

export const sendText = (item: BacklogItem) => `Do ${item.ref}`;

/**
 * Puts text into the orchestrator's chat draft for the user to read and send. It
 * never sends: the composer picks the draft up when the session opens. Existing
 * draft text stays, with the new text after it.
 */
export function prefillChatDraft(target: DraftTarget, text: string): boolean {
	const scope = { sessionId: target.id, incarnation: target.createdAt ?? target.id };
	if (!activateChatDraftScope(scope).ok) return false;
	const loaded = loadChatSessionDraft(scope);
	if (!loaded.ok) return false;
	const existing = loaded.draft.composer.text.trimEnd();
	return writeChatComposerText(scope, existing ? `${existing}\n\n${text}` : text).ok;
}
