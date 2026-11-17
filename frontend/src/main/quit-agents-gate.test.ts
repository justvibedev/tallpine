import { describe, expect, it, vi } from "vitest";
import type { QuitAgentsChoice } from "./quit-agents";
import { createQuitAgentsGate } from "./quit-agents-gate";

function setup(answer: QuitAgentsChoice | "cancel" | undefined, unattended = () => false) {
	const decide = vi.fn(async () => answer);
	const quit = vi.fn();
	const gate = createQuitAgentsGate({ decide, quit, unattended });
	return { gate, decide, quit };
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

describe("B753: every quit asks about running agents", () => {
	it("holds any quit (menu, ⌘Q, Dock, tray, logout) until the choice is made, then quits with it", async () => {
		const { gate, decide, quit } = setup("stop");
		const prevent = vi.fn();
		expect(gate.hold(prevent)).toBe(true);
		expect(prevent).toHaveBeenCalledOnce();
		// A second quit while the dialog is up waits too.
		expect(gate.hold(prevent)).toBe(true);
		await flush();
		expect(decide).toHaveBeenCalledOnce();
		expect(quit).toHaveBeenCalledOnce();
		expect(gate.choice()).toBe("stop");
		expect(gate.hold(vi.fn())).toBe(false);
	});

	it("never asks on a quit nobody can answer (signal, install hand-off)", () => {
		const { gate, decide, quit } = setup("stop");
		gate.quitUnattended();
		expect(quit).toHaveBeenCalledOnce();
		const prevent = vi.fn();
		expect(gate.hold(prevent)).toBe(false);
		expect(prevent).not.toHaveBeenCalled();
		expect(decide).not.toHaveBeenCalled();
		expect(gate.choice()).toBeUndefined();
	});

	it("never asks during an update restart", () => {
		const { gate, decide } = setup("stop", () => true);
		expect(gate.hold(vi.fn())).toBe(false);
		expect(decide).not.toHaveBeenCalled();
	});

	it("a signal while the dialog is up quits without waiting for it", () => {
		const { gate } = setup("stop");
		expect(gate.hold(vi.fn())).toBe(true);
		gate.quitUnattended();
		expect(gate.hold(vi.fn())).toBe(false);
	});

	it("cancel keeps the app open and asks again on the next quit", async () => {
		const { gate, decide, quit } = setup("cancel");
		expect(gate.hold(vi.fn())).toBe(true);
		await flush();
		expect(quit).not.toHaveBeenCalled();
		expect(gate.hold(vi.fn())).toBe(true);
		await flush();
		expect(decide).toHaveBeenCalledTimes(2);
	});

	it("quits with the default when nothing runs", async () => {
		const { gate, quit } = setup(undefined);
		gate.hold(vi.fn());
		await flush();
		expect(quit).toHaveBeenCalledOnce();
		expect(gate.choice()).toBeUndefined();
		expect(gate.hold(vi.fn())).toBe(false);
	});
});
