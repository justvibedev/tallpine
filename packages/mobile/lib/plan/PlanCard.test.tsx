import { act, create, type ReactTestInstance } from "react-test-renderer";
import { Text } from "react-native";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	Text: "Text", View: "View", Pressable: "Pressable", TextInput: "TextInput",
}));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "x" });
	return { useTheme: () => theme, useThemedStyles: (f: (t: unknown) => unknown) => f(theme) };
});
vi.mock("../ui", () => ({
	Button: ({ title, onPress, disabled }: { title: string; onPress: () => void; disabled?: boolean }) => ({ type: "Button", props: { title, onPress, disabled }, key: null, ref: null, $$typeof: Symbol.for("react.transitional.element") }),
}));
vi.mock("../textSelection", () => ({ textSelectionColors: () => ({}) }));
vi.mock("./PlanReader", () => ({ PlanReader: "PlanReader" }));

import { PlanCard } from "./PlanCard";
import type { PlanView } from "./planCardModel";

const base: PlanView = {
	id: "p1", status: "awaiting_answers", prSplit: [],
	questions: [{ id: "q1", text: "DB?", options: [{ id: "a", label: "SQLite" }, { id: "b", label: "Postgres" }], recommended: "b" }],
	assumptions: [{ id: "as1", text: "Go 1.22" }],
};

function render(plan: PlanView) {
	const fns = { onAnswer: vi.fn(), onDefaults: vi.fn(), onApprove: vi.fn(), onReplan: vi.fn(), onComment: vi.fn() };
	let r!: ReturnType<typeof create>;
	act(() => { r = create(<PlanCard itemRef="B1" itemTitle="Thing" plan={plan} {...fns} />); });
	return { r, ...fns };
}
const byLabel = (r: ReturnType<typeof create>, label: string | RegExp) =>
	r.root.findAll((n) => typeof n.props.accessibilityLabel === "string" && (typeof label === "string" ? n.props.accessibilityLabel === label : label.test(n.props.accessibilityLabel)))[0];
const openAssumptions = (r: ReturnType<typeof create>) => act(() => byLabel(r, /^Assumptions, /).props.onPress());
const button = (r: ReturnType<typeof create>, title: string): ReactTestInstance => r.root.findAll((n) => (n.type as unknown) === "Button" && n.props.title === title)[0];

describe("PlanCard", () => {
	const textsOf = (r: ReturnType<typeof create>) => r.root.findAllByType(Text).map((n: any) => [n.props.children].flat().join(""));

	it("says the planner waits for a usage reset, with the time, instead of writing", () => {
		const { r } = render({ ...base, status: "writing", pausedUntil: "2026-11-02T06:20:00Z" });
		const texts = textsOf(r);
		expect(texts).toContain("Waiting for usage reset");
		expect(texts.some((t: string) => /usage limit.*by itself at .*\d/.test(t))).toBe(true);
		expect(texts).not.toContain("Writing the plan from your answers.");
	});

	it("keeps the writing text while the planner is not paused", () => {
		const { r } = render({ ...base, status: "writing" });
		expect(textsOf(r)).toContain("Writing the plan from your answers.");
	});

	it("sends the recommended answers as defaults when nothing changed", () => {
		const { r, onDefaults, onAnswer } = render(base);
		act(() => button(r, "Send answers").props.onPress());
		expect(onDefaults).toHaveBeenCalledTimes(1);
		expect(onAnswer).not.toHaveBeenCalled();
	});

	it("sends a changed answer and a correction", () => {
		const { r, onAnswer } = render(base);
		const sqlite = r.root.findAll((n) => n.props.accessibilityLabel === "SQLite")[0];
		act(() => sqlite.props.onPress());
		openAssumptions(r);
		const fix = r.root.findAll((n) => n.props.accessibilityLabel === "Change: Go 1.22")[0];
		act(() => fix.props.onPress());
		const input = r.root.findAll((n) => n.props.accessibilityLabel === "Correction for Go 1.22")[0];
		act(() => input.props.onChangeText("Go 1.23"));
		act(() => button(r, "Send answers").props.onPress());
		expect(onAnswer).toHaveBeenCalledWith({ questions: { q1: { option: "a" } }, corrections: { as1: "Go 1.23" } });
	});

	it("shows the accepted-as-written count and flags scope cuts", () => {
		const { r } = render({ ...base, assumptions: [{ id: "as1", text: "Go 1.22" }, { id: "as2", text: "Windows support comes later", scopeCut: true }] });
		expect(textsOf(r)).toContain("2 assumptions will be accepted as written");
		expect(textsOf(r)).toContain("Includes a scope cut");
		openAssumptions(r);
		expect(textsOf(r)).toContain("Drops or postpones scope");
	});

	it("has an explicit Go with defaults button", () => {
		const { r, onDefaults } = render(base);
		act(() => button(r, "Go with defaults").props.onPress());
		expect(onDefaults).toHaveBeenCalledTimes(1);
	});

	it("approves a plan in review", () => {
		const { r, onApprove } = render({ ...base, status: "awaiting_approval" });
		act(() => button(r, "Approve plan").props.onPress());
		expect(onApprove).toHaveBeenCalledWith("", expect.any(Function));
	});

	it("keeps the assumptions collapsed until opened (B831)", () => {
		const { r } = render(base);
		expect(byLabel(r, /^Assumptions, 1/).props.accessibilityState).toEqual({ expanded: false });
		expect(textsOf(r)).not.toContain("Go 1.22");
		openAssumptions(r);
		expect(textsOf(r)).toContain("Go 1.22");
	});

	it("sends the comment alone, with Approve, or with Re-plan (B831)", () => {
		const { r, onApprove, onReplan, onComment } = render({ ...base, status: "awaiting_approval" });
		expect(button(r, "Comment").props.disabled).toBe(true);
		act(() => byLabel(r, "Comment on the whole plan").props.onChangeText("Keep the old flag"));
		act(() => button(r, "Comment").props.onPress());
		expect(onComment).toHaveBeenCalledWith("Keep the old flag", expect.any(Function));
		act(() => button(r, "Approve with comment").props.onPress());
		expect(onApprove).toHaveBeenCalledWith("Keep the old flag", expect.any(Function));
		act(() => button(r, "Re-plan with comment").props.onPress());
		expect(onReplan).toHaveBeenCalledWith("Keep the old flag", expect.any(Function));
		act(() => onComment.mock.calls[0][1]());
		expect(byLabel(r, "Comment on the whole plan").props.value).toBe("");
	});

	it("holds Approve while the planner answers a comment and lists the comments (B831)", () => {
		const { r } = render({ ...base, status: "awaiting_approval", comments: [{ id: "c1", text: "Rename it", by: "user", at: "x", outcome: "pending", approve: true }] });
		expect(textsOf(r)).toContain("Planner is answering your comment");
		expect(textsOf(r).some((t: string) => t.startsWith("Approved with your comment"))).toBe(true);
		expect(textsOf(r).some((t: string) => t.startsWith("Rename it"))).toBe(true);
		expect(textsOf(r)).toContain(" · Waiting for the planner");
		expect(button(r, "Approve plan").props.disabled).toBe(true);
	});
});
