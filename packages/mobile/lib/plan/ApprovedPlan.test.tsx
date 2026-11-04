import { act, create } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({ StyleSheet: { create: (s: unknown) => s }, Text: "Text", View: "View" }));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "x" });
	return { useThemedStyles: (f: (t: unknown) => unknown) => f(theme) };
});
vi.mock("../ui", () => ({ Button: "Button" }));
vi.mock("./PlanReader", () => ({ PlanReader: "PlanReader" }));
const getItemPlan = vi.fn();
vi.mock("./planApi", () => ({ getItemPlan: (...a: unknown[]) => getItemPlan(...a) }));

import { ApprovedPlan } from "./ApprovedPlan";

const cfg = {} as never;
async function render() {
	let r!: ReturnType<typeof create>;
	await act(async () => { r = create(<ApprovedPlan cfg={cfg} projectId="p" itemRef="B1" />); });
	return r;
}

describe("ApprovedPlan", () => {
	it("fetches the plan itself, so a list item without a plan field still gets Read plan", async () => {
		getItemPlan.mockResolvedValue({ id: "x", status: "approved", questions: [], assumptions: [], prSplit: [] });
		const r = await render();
		expect(getItemPlan).toHaveBeenCalledWith(cfg, "p", "B1");
		const btn = r.root.findAll((n) => (n.type as unknown) === "Button" && n.props.title === "Read plan")[0];
		expect(btn).toBeTruthy();
		await act(async () => btn.props.onPress());
		expect(r.root.findAll((n) => (n.type as unknown) === "PlanReader")).toHaveLength(1);
	});

	it("renders nothing without an approved plan", async () => {
		getItemPlan.mockResolvedValue(null);
		expect((await render()).toJSON()).toBeNull();
		getItemPlan.mockResolvedValue({ id: "x", status: "awaiting_approval", questions: [], assumptions: [], prSplit: [] });
		expect((await render()).toJSON()).toBeNull();
	});
});
