import { beforeEach, describe, expect, it } from "vitest";
import { backlogItem as item } from "../test/backlog-fixtures";
import { discussText, prefillChatDraft, sendText } from "./backlog-chat-draft";
import { loadChatSessionDraft } from "./chat-drafts";

const target = { id: "orch-1", createdAt: "2026-10-01T00:00:00Z" };
const scope = { sessionId: "orch-1", incarnation: "2026-10-01T00:00:00Z" };

beforeEach(() => window.localStorage.clear());

describe("discussText", () => {
	it("carries the whole item and asks for no building", () => {
		const text = discussText(item({ ref: "B4", title: "T", state: "open", hold: "later", parentRef: "B1", scope: "pr", note: "n", doneWhen: "d", agentNotes: "a", fields: { k: "v" } }));
		for (const want of ["B4", "· T (open, held: later)", "Note:\nn", "Done when: d", "Scope: pr", "Part of B1", "Agent notes:\na", "- k: v", "before anything is built"]) expect(text).toContain(want);
	});
	it("leaves out what the item does not have", () => {
		const text = discussText(item({ ref: "B4", title: "T" }));
		for (const absent of ["Note:", "Done when:", "Links:", "Agent notes:", "Fields:", "Source"]) expect(text).not.toContain(absent);
	});
});

describe("sendText", () => {
	it("is Do <ref>", () => expect(sendText(item({ ref: "B12" }))).toBe("Do B12"));
});

describe("prefillChatDraft", () => {
	it("writes the composer draft for the session incarnation the chat will open", () => {
		expect(prefillChatDraft(target, "Do B12")).toBe(true);
		expect(loadChatSessionDraft(scope).draft.composer.text).toBe("Do B12");
	});
	it("appends to what the user already typed instead of replacing it", () => {
		prefillChatDraft(target, "half a thought  ");
		prefillChatDraft(target, "Do B12");
		expect(loadChatSessionDraft(scope).draft.composer.text).toBe("half a thought\n\nDo B12");
	});
	it("scopes the draft by incarnation, so a replaced session does not inherit it", () => {
		prefillChatDraft(target, "Do B12");
		expect(loadChatSessionDraft({ sessionId: "orch-1", incarnation: "2026-11-01T00:00:00Z" }).draft.composer.text).toBe("");
	});
});
