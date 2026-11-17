import { describe, expect, it, vi } from "vitest";
import { createQuitConfirmController } from "./quit-confirm";

function setup(canConfirm = true) {
	const requestConfirm = vi.fn();
	const quit = vi.fn();
	const controller = createQuitConfirmController({ canConfirm: () => canConfirm, requestConfirm, quit });
	return { controller, requestConfirm, quit };
}

describe("createQuitConfirmController", () => {
	it("asks the shell to confirm on ⌘Q and quits once confirmed", () => {
		const { controller, requestConfirm, quit } = setup();

		controller.menuQuit(true);
		expect(requestConfirm).toHaveBeenCalledOnce();
		expect(quit).not.toHaveBeenCalled();

		controller.resolve(true);
		expect(quit).toHaveBeenCalledOnce();
	});

	it("quits on a second ⌘Q while the dialog is showing", () => {
		const { controller, requestConfirm, quit } = setup();

		controller.menuQuit(true);
		controller.menuQuit(true);
		expect(requestConfirm).toHaveBeenCalledOnce();
		expect(quit).toHaveBeenCalledOnce();
	});

	it("asks again after the dialog was cancelled or the shell reloaded", () => {
		const { controller, requestConfirm, quit } = setup();

		controller.menuQuit(true);
		controller.resolve(false);
		controller.menuQuit(true);
		controller.reset();
		controller.menuQuit(true);
		expect(requestConfirm).toHaveBeenCalledTimes(3);
		expect(quit).not.toHaveBeenCalled();
	});

	it("quits at once from a mouse click or when there is no shell to ask in", () => {
		const clicked = setup();
		clicked.controller.menuQuit(false);
		expect(clicked.quit).toHaveBeenCalledOnce();
		expect(clicked.requestConfirm).not.toHaveBeenCalled();

		const hidden = setup(false);
		hidden.controller.menuQuit(true);
		expect(hidden.quit).toHaveBeenCalledOnce();
		expect(hidden.requestConfirm).not.toHaveBeenCalled();
	});

	describe("B753: one confirmation when agents are running", () => {
		const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
		function withAgents(running: boolean) {
			const requestConfirm = vi.fn();
			const quit = vi.fn();
			const controller = createQuitConfirmController({
				canConfirm: () => true,
				requestConfirm,
				quit,
				quitWillAsk: async () => running,
			});
			return { controller, requestConfirm, quit };
		}

		it("⌘Q skips the shell's dialog when the quit will ask about running agents", async () => {
			const { controller, requestConfirm, quit } = withAgents(true);
			controller.menuQuit(true);
			await flush();
			expect(requestConfirm).not.toHaveBeenCalled();
			expect(quit).toHaveBeenCalledOnce();
		});

		it("⌘Q still asks in the shell when no agent is running", async () => {
			const { controller, requestConfirm, quit } = withAgents(false);
			controller.menuQuit(true);
			await flush();
			expect(requestConfirm).toHaveBeenCalledOnce();
			expect(quit).not.toHaveBeenCalled();
			controller.resolve(true);
			expect(quit).toHaveBeenCalledOnce();
		});

		it("a second ⌘Q while the check runs quits once", async () => {
			const { controller, requestConfirm, quit } = withAgents(false);
			controller.menuQuit(true);
			controller.menuQuit(true);
			await flush();
			expect(quit).toHaveBeenCalledOnce();
			expect(requestConfirm).not.toHaveBeenCalled();
		});
	});
});
