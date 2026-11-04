import { describe, expect, it } from "vitest";
import { emptySideChatDraft, type SideDeliveryReceipt } from "../components/chat/sideChatDraft";
import {
	MAX_SIDE_REFERENCES,
	mergeLatestSnapshot,
	nextActiveAfterClose,
	omitKey,
	parseSideCommand,
	selectActiveAfterRefresh,
	settleDeliveredDraft,
	upsertSide,
	withDraftAttachments,
	withDraftReference,
	withDraftText,
	withoutPendingDelivery,
	type Side,
	type Snapshot,
} from "./side-chats-model";

const side = (id: string, label = id) => ({ id, label, sessionId: "s" }) as unknown as Side;
const ref = (n: number) => ({ id: `r${n}`, conversationId: "c", messageId: `m${n}`, revision: 1, text: `t${n}`, role: "user" as const });
const receipt = (draft = emptySideChatDraft()): SideDeliveryReceipt => ({
	clientMessageId: "cm",
	request: { text: "q", clientMessageId: "cm" } as SideDeliveryReceipt["request"],
	submitted: { text: draft.text, attachments: draft.attachments, references: draft.references },
});

describe("side-chats-model: tab selection", () => {
	it("keeps the active side while listed, else the newest", () => {
		const sides = [side("a"), side("b")];
		expect(selectActiveAfterRefresh("a", sides)).toBe("a");
		expect(selectActiveAfterRefresh("gone", sides)).toBe("b");
		expect(selectActiveAfterRefresh(undefined, sides)).toBe("b");
		expect(selectActiveAfterRefresh("a", [])).toBeUndefined();
	});

	it("upserts by id, preserving order", () => {
		const sides = [side("a"), side("b")];
		expect(upsertSide(sides, side("b", "renamed")).map((s) => s.label)).toEqual(["a", "renamed"]);
		expect(upsertSide(sides, side("c")).map((s) => s.id)).toEqual(["a", "b", "c"]);
	});

	it("selects the neighbour at the same index after a close, clamped", () => {
		const sides = [side("a"), side("b"), side("c")];
		expect(nextActiveAfterClose(sides, "b")).toBe("c");
		expect(nextActiveAfterClose(sides, "c")).toBe("b");
		expect(nextActiveAfterClose(sides, "a")).toBe("b");
		expect(nextActiveAfterClose([side("a")], "a")).toBeUndefined();
	});
});

describe("side-chats-model: snapshot merge", () => {
	const snap = (sideId: string, turns: string[], hasMore = false): Snapshot =>
		({
			side: { id: sideId },
			hasMore,
			turns: turns.map((id) => ({ id, createdAt: `2026-01-0${id}` })),
			messages: turns.map((id) => ({ id: `m${id}` })),
			activities: [],
		}) as unknown as Snapshot;

	it("replaces the snapshot when the side changed", () => {
		const next = snap("b", ["1"]);
		expect(mergeLatestSnapshot(snap("a", ["1"]), next)).toBe(next);
		expect(mergeLatestSnapshot(undefined, next)).toBe(next);
	});

	it("keeps the previously loaded tail, de-duplicates, orders turns and keeps hasMore", () => {
		const merged = mergeLatestSnapshot(snap("a", ["1", "2"], true), snap("a", ["2", "3"], false));
		expect(merged.turns?.map((t) => t.id)).toEqual(["1", "2", "3"]);
		expect(merged.messages?.map((m) => m.id)).toEqual(["m1", "m2", "m3"]);
		expect(merged.hasMore).toBe(true);
	});
});

describe("side-chats-model: commands and drafts", () => {
	it("parses /btw and /side, returning the trimmed question", () => {
		expect(parseSideCommand("/btw what now ")).toEqual({ question: "what now" });
		expect(parseSideCommand("/SIDE")).toEqual({ question: "" });
		expect(parseSideCommand("/sidekick x")).toBeUndefined();
		expect(parseSideCommand("hello /btw")).toBeUndefined();
	});

	it("sets draft text without touching other drafts", () => {
		const drafts = { a: { ...emptySideChatDraft(), text: "keep" } };
		const next = withDraftText(drafts, "b", "hi");
		expect(next.a).toBe(drafts.a);
		expect(next.b?.text).toBe("hi");
	});

	it("adds a reference once, ignores duplicates, and reports the limit without applying it", () => {
		let drafts = withDraftReference({}, "a", ref(1));
		expect(drafts.outcome).toBe("added");
		const dup = withDraftReference(drafts.drafts, "a", ref(1));
		expect(dup.outcome).toBe("duplicate");
		expect(dup.drafts).toBe(drafts.drafts);
		let current = drafts.drafts;
		for (let n = 2; n <= MAX_SIDE_REFERENCES; n++) current = withDraftReference(current, "a", ref(n)).drafts;
		expect(current.a?.references).toHaveLength(MAX_SIDE_REFERENCES);
		const over = withDraftReference(current, "a", ref(99));
		expect(over.outcome).toBe("limit");
		expect(over.drafts).toBe(current);
	});

	it("appends attachments to the existing ones", () => {
		const att = (id: string) => ({ id, path: id, name: id, mimeType: "x", bytes: 1 });
		const once = withDraftAttachments({}, "a", [att("1")]);
		expect(withDraftAttachments(once, "a", [att("2")]).a?.attachments.map((x) => x.id)).toEqual(["1", "2"]);
	});

	it("omits a key without mutating the source", () => {
		const record = { a: 1, b: 2 };
		expect(omitKey(record, "a")).toEqual({ b: 2 });
		expect(record).toEqual({ a: 1, b: 2 });
	});
});

describe("side-chats-model: delivery receipts", () => {
	it("drops the pending receipt only when it is the same frozen one", () => {
		const frozen = receipt();
		const drafts = { a: { ...emptySideChatDraft(), pendingDelivery: frozen } };
		expect(withoutPendingDelivery(drafts, "a", frozen).a?.pendingDelivery).toBeUndefined();
		expect(withoutPendingDelivery(drafts, "a", receipt())).toBe(drafts);
		expect(withoutPendingDelivery(drafts, "missing", frozen)).toBe(drafts);
	});

	it("resets an unchanged draft after delivery and keeps edits made meanwhile", () => {
		const submitted = { ...emptySideChatDraft(), text: "q" };
		const frozen = receipt(submitted);
		const untouched = settleDeliveredDraft({ ...submitted, pendingDelivery: frozen }, frozen, undefined);
		expect(untouched.text).toBe("");
		expect(untouched.pendingDelivery).toBeUndefined();
		const edited = settleDeliveredDraft({ ...submitted, text: "q and more", pendingDelivery: frozen }, frozen, undefined);
		expect(edited.text).toBe("q and more");
		expect(edited.pendingDelivery).toBeUndefined();
	});

	it("records a handoff delivery so a remount can retry the exact request", () => {
		const frozen = receipt();
		const settled = settleDeliveredDraft({ ...emptySideChatDraft(), pendingDelivery: frozen }, frozen, "cm");
		expect(settled.lastHandoffDelivery).toBe(frozen);
		const previous = receipt();
		const kept = settleDeliveredDraft(
			{ ...emptySideChatDraft(), pendingDelivery: frozen, lastHandoffDelivery: previous },
			frozen,
			undefined,
		);
		expect(kept.lastHandoffDelivery).toBe(previous);
	});
});
