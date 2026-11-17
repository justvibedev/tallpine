import type { QuitAgentsChoice } from "./quit-agents";

/**
 * Holds every quit (app menu, ⌘Q, Dock, tray, OS logout) until the owner says
 * what happens to the agents still running (B753). Only quits nobody can answer
 * skip the question: signals, update restarts and the hand-off to a newer
 * install. They keep the default, which parks the agents of an app-owned daemon.
 */
export function createQuitAgentsGate(deps: {
	/** Resolves the owner's choice; undefined when nothing runs or the daemon cannot say. */
	decide(): Promise<QuitAgentsChoice | "cancel" | undefined>;
	quit(): void;
	/** True while a quit is under way that nobody can answer (an update restart). */
	unattended?(): boolean;
}) {
	let state: "idle" | "asking" | "decided" | "unattended" = "idle";
	let choice: QuitAgentsChoice | undefined;

	return {
		/** A quit nobody is there to answer: never ask. */
		quitUnattended(): void {
			if (state !== "decided") state = "unattended";
			deps.quit();
		},
		/** Called from before-quit; true means the quit was held and must wait. */
		hold(preventDefault: () => void): boolean {
			if (state === "asking") {
				preventDefault();
				return true;
			}
			if (state !== "idle" || deps.unattended?.()) return false;
			preventDefault();
			state = "asking";
			void deps
				.decide()
				.catch(() => undefined)
				.then((decided) => {
					if (decided === "cancel") {
						state = "idle";
						return;
					}
					state = "decided";
					choice = decided;
					deps.quit();
				});
			return true;
		},
		/** The owner's choice for this quit, if one was asked. */
		choice(): QuitAgentsChoice | undefined {
			return choice;
		},
	};
}
