import { act, create } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({ AppState: {}, View: "View" }));
vi.mock("./planPolling", () => ({ startPlanPolling: (fn: () => void) => { fn(); return () => {}; } }));
vi.mock("../connectionError", () => ({ userFacingError: String }));
vi.mock("../tokens", () => ({ space: {} }));
vi.mock("./PlanCard", () => ({ PlanCard: "PlanCard" }));
const listProjectPlans = vi.fn();
vi.mock("./planApi", () => ({
	listProjectPlans: (...a: unknown[]) => listProjectPlans(...a),
	acceptPlanDefaults: vi.fn(), answerPlan: vi.fn(), approvePlan: vi.fn(),
}));

import { useProjectPlans } from "./PlanCards";

const cfg = {} as never;
const plan = (id: string) => [{ itemRef: "B12", plan: { id } }] as never;

function Probe({ projectId, out }: { projectId: string; out: { ids: string[] } }) {
	out.ids = useProjectPlans(cfg, projectId).plans.map((p) => (p.plan as unknown as { id: string }).id);
	return null;
}

function deferred() {
	listProjectPlans.mockReset();
	const res: Record<string, (v: unknown) => void> = {};
	listProjectPlans.mockImplementation((_c: unknown, id: string) => new Promise((r) => { res[id] = r; }));
	return res;
}

describe("useProjectPlans", () => {
	it("hides the old project's plans on switch", async () => {
		const res = deferred();
		const out = { ids: [] as string[] };
		let r!: ReturnType<typeof create>;
		await act(async () => { r = create(<Probe projectId="A" out={out} />); });
		await act(async () => { res.A(plan("planA")); });
		expect(out.ids).toEqual(["planA"]);
		await act(async () => { r.update(<Probe projectId="B" out={out} />); });
		expect(out.ids).toEqual([]);
		await act(async () => { res.B(plan("planB")); });
		expect(out.ids).toEqual(["planB"]);
	});

	it("drops a response for the old project that resolves after the switch", async () => {
		const res = deferred();
		const out = { ids: [] as string[] };
		let r!: ReturnType<typeof create>;
		await act(async () => { r = create(<Probe projectId="A" out={out} />); }); // A stays in flight
		await act(async () => { r.update(<Probe projectId="B" out={out} />); });
		await act(async () => { res.B(plan("planB")); });
		expect(out.ids).toEqual(["planB"]);
		await act(async () => { res.A(plan("planA-late")); });
		expect(out.ids).toEqual(["planB"]);
	});
});
