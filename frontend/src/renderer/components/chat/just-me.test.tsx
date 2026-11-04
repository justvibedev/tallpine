import { act, fireEvent, render, renderHook, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useChatJustMeStore, justMeStorageKey } from "../../stores/chat-just-me-store";
import { JustMeToggle } from "./JustMeToggle";
import { JustMeRunRow } from "./JustMeRows";
import { useUnreadReplies } from "./useUnreadReplies";
import type { ConversationActivity, ConversationMessage } from "../../types/conversation";
import { justMeGroups } from "./just-me";
import { chatLastSeen, ownerReplies } from "./chat-last-seen";
import type { TimelineGroup } from "./timeline-groups";

const createdAt = "2026-11-04T10:00:00Z";
const msg = (o: Partial<ConversationMessage>): ConversationMessage =>
	({ kind: "message", id: `m${o.sequence}`, sequence: 1, revision: 0, role: "user", origin: "human", text: "hi", streaming: false, createdAt, ...o });
const tool = (o: Partial<ConversationActivity>): ConversationActivity =>
	({ kind: "activity", id: `a${o.sequence}`, sequence: 1, revision: 0, activityKind: "command", status: "completed", summary: "ls", createdAt, ...o });
const group = (key: string, items: TimelineGroup["items"], extra: Partial<TimelineGroup> = {}): TimelineGroup =>
	({ key, anchor: items[0]?.sequence ?? 0, items, ...extra });

const owner = group("t1", [msg({ sequence: 1 }), tool({ sequence: 2 }), msg({ sequence: 3, role: "assistant", text: "answer" })]);
const report1 = group("t2", [msg({ sequence: 4, origin: "automation", text: "[done] worker" }), msg({ sequence: 5, role: "assistant", text: "noted" })]);
const report2 = group("t3", [msg({ sequence: 6, origin: "automation", text: "[auto] notice" })], { live: true });
const owner2 = group("t4", [msg({ sequence: 7 }), msg({ sequence: 8, role: "assistant", text: "second" })]);

describe("justMeGroups", () => {
	it("keeps owner turns' messages only and folds orchestration runs into one counted line", () => {
		const out = justMeGroups([owner, report1, report2, owner2], new Set());
		expect(out.map((g) => g.key)).toEqual(["t1", "just-me-turn:t1", "just-me:t2", "t4"]);
		expect(out[0].items.map((i) => i.sequence)).toEqual([1, 3]);
		expect(out[1].collapsedRun).toEqual({ count: 1, open: false });
		expect(out[2]).toMatchObject({ collapsedRun: { count: 2, open: false }, live: true });
		expect(out[3]).toBe(owner2);
	});

	it("an owner turn's folded tool rows expand back in place", () => {
		const out = justMeGroups([owner], new Set(["just-me-turn:t1"]));
		expect(out[0]).toBe(owner);
		expect(out[1].collapsedRun).toEqual({ count: 1, open: true });
	});

	it("an expanded run keeps its line and shows its turns", () => {
		const out = justMeGroups([owner, report1, report2], new Set(["just-me:t2"]));
		expect(out.map((g) => g.key)).toEqual(["t1", "just-me-turn:t1", "just-me:t2", "t2", "t3"]);
		expect(out[2].collapsedRun).toEqual({ count: 2, open: true });
	});

	it("keeps a worker turn whose reply carries the Needs you marker, folds an unmarked one", () => {
		const marked = group("t6", [msg({ sequence: 10, origin: "automation", text: "Reports since your previous turn:\n\n[done] [Check](tallpine://sessions/proj/proj-288)\nAll good." }), msg({ sequence: 11, role: "assistant", text: "**Needs you:** tell me works, merge" })]);
		expect(justMeGroups([report1, marked], new Set()).map((g) => g.key)).toContain("t6");
		const plain = group("t7", [msg({ sequence: 12, origin: "automation", text: "Reports since your previous turn:\n\n[done] [Check](tallpine://sessions/proj/proj-288)\nAll good."}), msg({ sequence: 13, role: "assistant", text: "Noted." })]);
		expect(justMeGroups([report1, plain], new Set()).map((g) => g.key)).toEqual(["just-me:t2"]);
	});

	it("never folds a turn waiting on the owner's decision", () => {
		const approval = group("t5", [tool({ sequence: 9, activityKind: "approval", status: "pending" })]);
		expect(justMeGroups([report1, approval], new Set()).map((g) => g.key)).toEqual(["just-me:t2", "t5"]);
	});
});

describe("unread replies", () => {
	it("counts only assistant replies in the owner's turns", () => {
		expect(ownerReplies([owner, report1, owner2])).toEqual([{ sequence: 3, index: 0 }, { sequence: 8, index: 2 }]);
	});

	it("persists the last seen reply per chat", () => {
		chatLastSeen.set("a", 3);
		chatLastSeen.set("b", 8);
		expect(chatLastSeen.get("a")).toBe(3);
		expect(chatLastSeen.get("b")).toBe(8);
		expect(chatLastSeen.get("c")).toBeUndefined();
	});
});

describe("Just me toggle", () => {
	it("persists the choice", () => {
		window.localStorage.removeItem(justMeStorageKey);
		useChatJustMeStore.setState({ justMe: false });
		render(<JustMeToggle />);
		const button = screen.getByRole("button", { name: "Just me" });
		fireEvent.click(button);
		expect(button.getAttribute("aria-pressed")).toBe("true");
		expect(window.localStorage.getItem(justMeStorageKey)).toBe("true");
	});

	it("a folded run reads as one line with its count and live state", () => {
		const onToggle = vi.fn();
		render(<JustMeRunRow count={3} open={false} live onToggle={onToggle} />);
		fireEvent.click(screen.getByRole("button", { name: /3 updates hidden/ }));
		expect(onToggle).toHaveBeenCalled();
		expect(screen.getByText("Working…")).toBeTruthy();
	});
});

describe("useUnreadReplies", () => {
	beforeEach(() => {
		window.localStorage.clear();
		vi.spyOn(document, "hasFocus").mockReturnValue(true);
	});

	function setup(groups: TimelineGroup[], pinned: boolean, virtualized = true) {
		const scrollToIndex = vi.fn();
		const setPinnedNow = vi.fn();
		const scroller = { current: document.createElement("div") };
		const scrollContent = { current: document.createElement("div") };
		const hook = renderHook((props: { groups: TimelineGroup[]; pinned: boolean }) => useUnreadReplies({
			memoryKey: "chat", groups: props.groups, pinned: props.pinned, setPinnedNow, markWritten: () => {},
			scroller, scrollContent, virtualized, virtualizer: { scrollToIndex }, layoutKey: props.groups.length,
		}), { initialProps: { groups, pinned } });
		return { ...hook, scrollToIndex, setPinnedNow };
	}

	it("a first visit marks everything seen and does not move the reader", () => {
		const { result, scrollToIndex } = setup([owner, report1, owner2], true);
		expect(result.current.unreadCount).toBe(0);
		expect(scrollToIndex).not.toHaveBeenCalled();
		expect(chatLastSeen.get("chat")).toBe(8);
	});

	it("returning lands on the first unseen reply and counts the unseen ones", () => {
		chatLastSeen.set("chat", 2);
		const { result, scrollToIndex, setPinnedNow } = setup([owner, report1, owner2], true);
		expect(scrollToIndex).toHaveBeenCalledWith(0, { align: "start" });
		expect(setPinnedNow).toHaveBeenCalledWith(false);
		expect(result.current.unreadCount).toBe(2);
	});

	it("a reply arriving at the end of a background window stays unread, and returning lands on it", () => {
		chatLastSeen.set("chat", 3);
		const focus = vi.spyOn(document, "hasFocus").mockReturnValue(false);
		const { result, rerender, scrollToIndex } = setup([owner], true);
		rerender({ groups: [owner, report1, owner2], pinned: true });
		expect(result.current.unreadCount).toBe(1);
		expect(chatLastSeen.get("chat")).toBe(3);
		focus.mockReturnValue(true);
		act(() => { window.dispatchEvent(new Event("focus")); });
		expect(scrollToIndex).toHaveBeenLastCalledWith(2, { align: "start" });
		expect(result.current.unreadCount).toBe(1);
	});

	it("reaching the end marks the replies seen", () => {
		chatLastSeen.set("chat", 3);
		vi.useFakeTimers({ toFake: ["performance"] });
		try {
			const { result, rerender } = setup([owner, report1, owner2], false);
			expect(result.current.unreadCount).toBe(1);
			act(() => vi.advanceTimersByTime(10_000));
			rerender({ groups: [owner, report1, owner2], pinned: true });
			expect(result.current.unreadCount).toBe(0);
			expect(chatLastSeen.get("chat")).toBe(8);
		} finally {
			vi.useRealTimers();
		}
	});
});
