// Like most macOS apps, ⌘Q asks before quitting: the shell shows a "Quit Tallpine?"
// dialog and reports the answer back. A second ⌘Q while that dialog is up quits,
// and choosing Quit from the menu with the mouse never asks. When agents are
// still running, the quit's own "N sessions are still running" dialog is the
// one confirmation, so ⌘Q skips the shell's (B753).
export function createQuitConfirmController(options: {
	/** False when there is no visible shell to show the dialog in. */
	canConfirm(): boolean;
	requestConfirm(): void;
	quit(): void;
	/** Resolves true when quitting will ask about running agents anyway. */
	quitWillAsk?(): Promise<boolean>;
}) {
	let pending = false;

	return {
		menuQuit(fromShortcut: boolean): void {
			if (!fromShortcut || pending || !options.canConfirm()) {
				pending = false;
				options.quit();
				return;
			}
			pending = true;
			if (!options.quitWillAsk) {
				options.requestConfirm();
				return;
			}
			void options
				.quitWillAsk()
				.catch(() => false)
				.then((willAsk) => {
					// A second ⌘Q already quit, or the shell reloaded meanwhile.
					if (!pending) return;
					if (!willAsk) {
						options.requestConfirm();
						return;
					}
					pending = false;
					options.quit();
				});
		},
		resolve(confirmed: boolean): void {
			pending = false;
			if (confirmed) options.quit();
		},
		/** The shell reloaded, so any dialog it was showing is gone. */
		reset(): void {
			pending = false;
		},
	};
}
