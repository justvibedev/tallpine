import { act, create } from "react-test-renderer";
import { beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const h = vi.hoisted(() => ({
	instances: [] as Array<{ handlers: Record<string, (...a: any[]) => void>; openTerminal: any; resize: any; sendInput: any; disconnect: any; connect: any }>,
}));
vi.mock("../mux", () => ({
	MuxClient: class {
		handlers: Record<string, (...a: any[]) => void>;
		openTerminal = vi.fn();
		resize = vi.fn();
		sendInput = vi.fn();
		disconnect = vi.fn();
		connect = vi.fn();
		constructor(_cfg: unknown, handlers: Record<string, (...a: any[]) => void>) {
			this.handlers = handlers;
			h.instances.push(this as never);
		}
	},
}));
vi.mock("../config", () => ({ isConfigured: () => true }));

import { useTerminalBridge } from "./useTerminalBridge";

type Bridge = ReturnType<typeof useTerminalBridge>;
const cfg = { url: "http://x" } as never;

async function mount(props: { activeConfig?: unknown; scheme?: string } = {}) {
	let api!: Bridge;
	function Harness(p: { scheme: string }) {
		api = useTerminalBridge({ activeConfig: ("activeConfig" in props ? props.activeConfig : cfg) as never, terminalHandleId: "t1", projectId: "p1", scheme: p.scheme, style: "s" });
		return null;
	}
	let r!: ReturnType<typeof create>;
	await act(async () => { r = create(<Harness scheme={props.scheme ?? "dark"} />); });
	const xterm = { write: vi.fn(), resize: vi.fn() };
	api.xtermRef.current = xterm as never;
	return { get api() { return api; }, xterm, mux: () => h.instances[h.instances.length - 1], r, rerender: (scheme: string) => act(async () => { r.update(<Harness scheme={scheme} />); }) };
}
const bytes = (n: number) => new Uint8Array([n]);

describe("useTerminalBridge", () => {
	beforeEach(() => { h.instances.length = 0; });

	it("buffers output before onInitialized and flushes it in wire order", async () => {
		const m = await mount();
		m.mux().handlers.onTerminalData("t1", bytes(1));
		m.mux().handlers.onTerminalData("t1", bytes(2));
		expect(m.xterm.write).not.toHaveBeenCalled();
		act(() => m.api.onInitialized());
		expect(m.xterm.write.mock.calls.map((c) => c[0][0])).toEqual([1, 2]);
		m.mux().handlers.onTerminalData("t1", bytes(3));
		expect(m.xterm.write).toHaveBeenCalledTimes(3);
	});

	it("ignores frames for other terminal handles", async () => {
		const m = await mount();
		act(() => m.api.onInitialized());
		m.mux().handlers.onTerminalData("other", bytes(9));
		m.mux().handlers.onTerminalResize("other", 10, 5);
		m.mux().handlers.onTerminalExited("other", 0);
		expect(m.xterm.write).not.toHaveBeenCalled();
		expect(m.api.size).toBeNull();
		expect(m.api.notFound).toBe(false);
	});

	it("opens the terminal once even if onInitialized fires twice, then sends known dims", async () => {
		const m = await mount();
		
		m.mux().handlers.onStatus("open");
		act(() => m.api.logger.log("x", "FRESSH_DIMS 80 24"));
		act(() => m.api.onInitialized());
		act(() => m.api.onInitialized());
		expect(m.mux().openTerminal).toHaveBeenCalledTimes(1);
		expect(m.mux().openTerminal).toHaveBeenCalledWith("t1", "p1");
		expect(m.mux().resize).toHaveBeenCalledWith("t1", 80, 24, "p1");
	});

	it("restores the daemon's authoritative grid onto a fresh xterm and stops following the local fit", async () => {
		const m = await mount();
		m.mux().handlers.onTerminalResize("t1", 100, 30);
		expect(m.xterm.resize).toHaveBeenCalledWith({ cols: 100, rows: 30 });
		m.xterm.resize.mockClear();
		act(() => m.api.logger.log("x", "FRESSH_DIMS 50 20"));
		expect(m.xterm.resize).not.toHaveBeenCalled(); // authoritative grid wins
		act(() => m.api.onInitialized());
		expect(m.xterm.resize).toHaveBeenCalledWith({ cols: 100, rows: 30 });
	});

	it("follows the local fit until the daemon sends a grid", async () => {
		const m = await mount();
		act(() => m.api.logger.log("x", "FRESSH_DIMS 50 20"));
		expect(m.xterm.resize).toHaveBeenCalledWith({ cols: 50, rows: 20 });
		expect(m.api.size).toEqual({ cols: 50, rows: 20 });
	});

	it("marks the terminal gone on exit / 'not found' and banners other errors", async () => {
		const m = await mount();
		m.mux().handlers.onTerminalExited("t1", 0);
		await act(async () => {});
		expect(m.api.notFound).toBe(true);
		expect(m.api.banner).toBeTruthy();
	});

	it("disconnects on unmount and drops buffered output on theme change", async () => {
		const m = await mount();
		const first = m.mux();
		m.mux().handlers.onTerminalData("t1", bytes(1));
		await m.rerender("light"); // scheme change resets the ready flag, effect keeps the mux
		act(() => m.api.onInitialized());
		expect(m.xterm.write).toHaveBeenCalledTimes(1);
		await act(async () => { m.r.unmount(); });
		expect(first.disconnect).toHaveBeenCalled();
	});

	it("builds no mux without a config", async () => {
		const m = await mount({ activeConfig: null });
		expect(h.instances.length).toBe(0);
		void m;
	});
});
