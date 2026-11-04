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
	const fns = { onAnswer: vi.fn(), onDefaults: vi.fn(), onApprove: vi.fn() };
	let r!: ReturnType<typeof create>;
	act(() => { r = create(<PlanCard itemRef="B1" itemTitle="Thing" plan={plan} {...fns} />); });
	return { r, ...fns };
}
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
		const fix = r.root.findAll((n) => n.props.accessibilityLabel === "Change: Go 1.22")[0];
		act(() => fix.props.onPress());
		const input = r.root.findAll((n) => n.props.accessibilityLabel === "Correction for Go 1.22")[0];
		act(() => input.props.onChangeText("Go 1.23"));
		act(() => button(r, "Send answers").props.onPress());
		expect(onAnswer).toHaveBeenCalledWith({ questions: { q1: { option: "a" } }, corrections: { as1: "Go 1.23" } });
	});

	it("shows the accepted-as-written count and flags scope cuts", () => {
		const { r } = render({ ...base, assumptions: [{ id: "as1", text: "Go 1.22" }, { id: "as2", text: "Windows support comes later", scopeCut: true }] });
		const texts = r.root.findAllByType(Text).map((n: any) => [n.props.children].flat().join(""));
		expect(texts).toContain("2 assumptions will be accepted as written");
		expect(texts).toContain("Drops or postpones scope");
	});

	it("has an explicit Go with defaults button", () => {
		const { r, onDefaults } = render(base);
		act(() => button(r, "Go with defaults").props.onPress());
		expect(onDefaults).toHaveBeenCalledTimes(1);
	});

	it("approves a plan in review", () => {
		const { r, onApprove } = render({ ...base, status: "awaiting_approval" });
		act(() => button(r, "Approve plan").props.onPress());
		expect(onApprove).toHaveBeenCalledTimes(1);
	});
});
