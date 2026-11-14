import { act, create, type ReactTestInstance } from "react-test-renderer";
import { beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({
	AppState: {},
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	Text: "Text", View: "View", Pressable: "Pressable", TextInput: "TextInput", ScrollView: "ScrollView",
}));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "x" });
	return { useTheme: () => theme, useThemedStyles: (f: (t: unknown) => unknown) => f(theme) };
});
vi.mock("../ui", () => ({
	Button: ({ title, onPress, disabled }: { title: string; onPress: () => void; disabled?: boolean }) => ({ type: "Button", props: { title, onPress, disabled }, key: null, ref: null, $$typeof: Symbol.for("react.transitional.element") }),
}));
vi.mock("../textSelection", () => ({ textSelectionColors: () => ({}) }));
vi.mock("../connectionError", () => ({ userFacingError: String }));
vi.mock("../tokens", () => ({ space: {}, radius: {}, type: new Proxy({}, { get: () => ({}) }) }));
vi.mock("../plan/planPolling", () => ({ startPlanPolling: (fn: () => void) => { fn(); return () => {}; } }));
const listProjectAsks = vi.fn();
const answerAsk = vi.fn();
vi.mock("./askApi", () => ({
	listProjectAsks: (...a: unknown[]) => listProjectAsks(...a),
	answerAsk: (...a: unknown[]) => answerAsk(...a),
}));

import { AskStrip } from "./AskStrip";
import type { OwnerAsk } from "./askModel";

const cfg = {} as never;
const ask = (o: Partial<OwnerAsk> & { ref: string }): OwnerAsk => ({
	id: o.ref, text: `Question ${o.ref}`, options: [{ id: "a", label: "Yes" }, { id: "b", label: "No" }], recommended: "a", state: "open", ...o,
});

async function mount(lane?: string) {
	let r!: ReturnType<typeof create>;
	await act(async () => { r = create(<AskStrip cfg={cfg} projectId="p1" lane={lane} />); });
	return r;
}
const text = (n: ReactTestInstance) => n.findAllByType("Text" as never).map((t) => t.children.join("")).join("|");
const press = async (n: ReactTestInstance) => { await act(async () => { await n.props.onPress(); }); };
const byLabel = (r: ReturnType<typeof create>, label: string) => r.root.findAll((n) => n.props.accessibilityLabel === label && typeof n.props.onPress === "function")[0];

beforeEach(() => { listProjectAsks.mockReset(); answerAsk.mockReset(); answerAsk.mockResolvedValue({ ask: {}, deliveryFailed: false }); });

describe("AskStrip", () => {
	it("is hidden when no ask is open", async () => {
		listProjectAsks.mockResolvedValue([ask({ ref: "Q1", state: "answered" }), ask({ ref: "Q2", state: "withdrawn" })]);
		const r = await mount();
		expect(r.toJSON()).toBeNull();
	});

	it("lists this chat's open asks; a lane ask only points from the main chat", async () => {
		listProjectAsks.mockResolvedValue([ask({ ref: "Q1" }), ask({ ref: "Q2", lane: "B7" })]);
		const main = await mount();
		expect(main.root.findAllByProps({ testID: "ask-card" })).toHaveLength(1);
		expect(text(main.root)).toContain("Q1 Question Q1");
		expect(main.root.findAllByProps({ testID: "ask-lane-pointer" })).toHaveLength(1);
		const lane = await mount("B7");
		expect(text(lane.root)).toContain("Q2 Question Q2");
		expect(text(lane.root)).not.toContain("Q1 Question");
		expect(lane.root.findAllByProps({ testID: "ask-lane-pointer" })).toHaveLength(0);
	});

	it("a tap on an option answers and refetches", async () => {
		listProjectAsks.mockResolvedValue([ask({ ref: "Q1" })]);
		const r = await mount();
		listProjectAsks.mockClear();
		await press(byLabel(r, "No"));
		expect(answerAsk).toHaveBeenCalledWith(cfg, "p1", "Q1", { option: "b" });
		expect(listProjectAsks).toHaveBeenCalledTimes(1);
	});

	it("Other sends the typed text and refetches", async () => {
		listProjectAsks.mockResolvedValue([ask({ ref: "Q1" })]);
		const r = await mount();
		await press(byLabel(r, "Other"));
		const input = r.root.findByType("TextInput" as never);
		await act(async () => { input.props.onChangeText("something else"); });
		listProjectAsks.mockClear();
		const send = r.root.findAll((n) => n.props.title === "Send")[0];
		await act(async () => { await send.props.onPress(); });
		expect(answerAsk).toHaveBeenCalledWith(cfg, "p1", "Q1", { other: "something else" });
		expect(listProjectAsks).toHaveBeenCalledTimes(1);
	});

	it("shows why an ask was repeated and the earlier answer", async () => {
		listProjectAsks.mockResolvedValue([
			ask({ ref: "Q1", state: "answered", answer: { text: "Postgres" } }),
			ask({ ref: "Q2", reaskOf: "Q1", reaskReason: "schema changed" }),
		]);
		const r = await mount();
		expect(text(r.root)).toContain("Asked again: schema changed");
		expect(text(r.root)).toContain("Earlier Q1 you answered: Postgres");
	});

	it("collapses to its header but stays while asks are open", async () => {
		listProjectAsks.mockResolvedValue([ask({ ref: "Q1" })]);
		const r = await mount();
		await press(r.root.findByProps({ testID: "ask-strip-header" }));
		expect(r.root.findAllByProps({ testID: "ask-card" })).toHaveLength(0);
		expect(text(r.root)).toContain("Waiting on you · 1");
	});

	it("keeps a notice after an answer that was saved but not delivered", async () => {
		listProjectAsks.mockResolvedValue([ask({ ref: "Q1" })]);
		answerAsk.mockResolvedValue({ ask: {}, deliveryFailed: true });
		const r = await mount();
		listProjectAsks.mockResolvedValue([]); // the answered ask leaves the strip
		await press(byLabel(r, "Yes"));
		expect(r.root.findAllByProps({ testID: "ask-card" })).toHaveLength(0);
		expect(text(r.root)).toContain("Your answer to Q1 was saved but not delivered");
	});
});
