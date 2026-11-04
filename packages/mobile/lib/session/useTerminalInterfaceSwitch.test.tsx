import { act, create } from "react-test-renderer";
import { beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const h = vi.hoisted(() => ({ state: { transition: undefined as any, start: vi.fn(), refresh: vi.fn(async () => {}), starting: false, status: undefined as any } }));
vi.mock("react-native", () => ({ Alert: { alert: vi.fn() }, Platform: { OS: "ios" } }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), warning: vi.fn(), success: vi.fn(), error: vi.fn() } }));
vi.mock("./useInterfaceTransition", async () => {
	const actual = await vi.importActual<typeof import("./interfaceTransition")>("./interfaceTransition");
	return {
		...actual,
		interfaceSwitchAlert: vi.fn(() => ({ title: "t", message: "m" })),
		useInterfaceTransition: () => h.state,
	};
});

import { useTerminalInterfaceSwitch } from "./useTerminalInterfaceSwitch";

function mount(known: any = null) {
	const out: { api?: ReturnType<typeof useTerminalInterfaceSwitch> } = {};
	const setBanner = vi.fn();
	function Harness() {
		out.api = useTerminalInterfaceSwitch({ activeConfig: {} as never, shellOnly: false, sessionId: "s", refresh: vi.fn() as never, known, setBanner });
		return null;
	}
	act(() => { create(<Harness />); });
	return { get api() { return out.api!; }, setBanner };
}

describe("useTerminalInterfaceSwitch", () => {
	beforeEach(() => { h.state.transition = undefined; h.state.starting = false; h.state.start = vi.fn(); });

	it("derives a failure notice only for unacknowledged failed/recovery phases", () => {
		h.state.transition = { id: "1", phase: "failed", errorDetail: "boom" };
		expect(mount().api.interfaceTransitionNotice?.id).toBe("1");
		expect(mount().api.interfaceTransitionNoticeText).toBe("boom");
		h.state.transition = { id: "1", phase: "failed", noticeAcknowledgedAt: "x" };
		expect(mount().api.interfaceTransitionNotice).toBeUndefined();
		h.state.transition = { id: "1", phase: "recovery_required", errorCode: "DAEMON_RESTARTED" };
		expect(mount().api.interfaceTransitionNoticeText).toMatch(/restored the session/);
	});

	it("is busy only for working / needing input / active sessions", () => {
		expect(mount({ status: "working" }).api.interfaceBusy).toBe(true);
		expect(mount({ activity: "waiting_input" }).api.interfaceBusy).toBe(true);
		expect(mount({ status: "idle", activity: "idle" }).api.interfaceBusy).toBe(false);
		expect(mount(null).api.interfaceBusy).toBe(false);
	});

	it("starts nothing on recheck when a request is already starting", async () => {
		h.state.starting = true;
		const m = mount();
		await act(async () => { await m.api.requestInterfaceSwitch(); });
		expect(h.state.start).not.toHaveBeenCalled();
		expect(h.state.refresh).not.toHaveBeenCalled();
	});
});
