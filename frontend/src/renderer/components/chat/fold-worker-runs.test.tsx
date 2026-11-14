import { fireEvent, render, renderHook, screen, act } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useChatJustMeStore } from "../../stores/chat-just-me-store";
import type { ConversationActivity, ConversationMessage } from "../../types/conversation";
import { JustMeRunRow } from "./JustMeRows";
import type { TimelineGroup } from "./timeline-groups";
import { useJustMeGroups } from "./useJustMeGroups";

const createdAt = "2026-11-04T10:00:00Z";
const msg = (o: Partial<ConversationMessage>): ConversationMessage =>
	({ kind: "message", id: `m${o.sequence}`, sequence: 1, revision: 0, role: "user", origin: "human", text: "hi", streaming: false, createdAt, ...o });
const act_ = (o: Partial<ConversationActivity>): ConversationActivity =>
	({ kind: "activity", id: `a${o.sequence}`, sequence: 1, revision: 0, activityKind: "command", status: "completed", summary: "ls", createdAt, ...o });
const group = (key: string, items: TimelineGroup["items"]): TimelineGroup => ({ key, anchor: items[0]?.sequence ?? 0, items });
const auto = (key: string, seq: number, text: string) => group(key, [msg({ sequence: seq, origin: "automation", text })]);

const w1 = auto("w1", 1, "[auto] first");
const w2 = auto("w2", 2, "[auto] newest status");
const ownerMsg = group("o1", [msg({ sequence: 3 })]);

function run(groups: TimelineGroup[]) {
	return renderHook(() => useJustMeGroups(groups));
}

describe("default view worker folding", () => {
	beforeEach(() => useChatJustMeStore.setState({ justMe: false }));

	it("folds 2+ consecutive worker groups into one row with the newest headline", () => {
		const { result } = run([ownerMsg, w1, w2]);
		expect(result.current.groups.map((g) => g.key)).toEqual(["o1", "worker-run:w1"]);
		expect(result.current.groups[1].collapsedRun).toEqual({ count: 2, open: false, workers: true, headline: "newest status" });
	});

	it("does not fold a single worker group", () => {
		const { result } = run([ownerMsg, w1]);
		expect(result.current.groups.map((g) => g.key)).toEqual(["o1", "w1"]);
	});

	it.each([
		["owner message", ownerMsg],
		["ask activity", group("ask", [msg({ sequence: 3, origin: "automation", text: "[auto] x" }), act_({ sequence: 4, activityKind: "system", detail: { event: "ask", ask: { id: "1", ref: "Q1" } } })])],
		["Needs you reply", group("nu", [msg({ sequence: 3, origin: "automation", text: "Reports since your previous turn:\n\n[done] [C](tallpine://sessions/p/p-1)\nok" }), msg({ sequence: 4, role: "assistant", text: "**Needs you:** pick" })])],
		["Needs you reply to an [auto] notice", group("nu2", [msg({ sequence: 3, origin: "automation", text: "[auto] worker idle" }), msg({ sequence: 4, role: "assistant", text: "**Needs you:** pick A or B" })])],
		["pending approval", group("ap", [act_({ sequence: 3, activityKind: "approval", status: "pending" })])],
	])("a %s breaks the run and never folds", (_n, breaker) => {
		const w3 = auto("w3", 10, "[auto] third");
		const { result } = run([w1, w2, breaker, w3, auto("w4", 11, "[auto] fourth")]);
		expect(result.current.groups.map((g) => g.key)).toEqual(["worker-run:w1", breaker.key, "worker-run:w3"]);
	});

	it("expands and collapses", () => {
		const { result } = run([w1, w2]);
		act(() => result.current.toggleRun("worker-run:w1"));
		expect(result.current.groups.map((g) => g.key)).toEqual(["worker-run:w1", "w1", "w2"]);
		expect(result.current.groups[0].collapsedRun?.open).toBe(true);
		act(() => result.current.toggleRun("worker-run:w1"));
		expect(result.current.groups.map((g) => g.key)).toEqual(["worker-run:w1"]);
	});
});

describe("worker run row", () => {
	it("labels the count and headline", () => {
		render(<JustMeRunRow count={3} open={false} live={false} workers headline="Tests green" onToggle={() => undefined} />);
		const button = screen.getByRole("button");
		expect(button.textContent).toContain("3");
		expect(button.textContent).toContain("Tests green");
		fireEvent.click(button);
	});
});
