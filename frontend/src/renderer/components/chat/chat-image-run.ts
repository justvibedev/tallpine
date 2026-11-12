import type { useUiStore } from "../../stores/ui-store";

type Toast = ReturnType<typeof useUiStore.getState>["showGlobalToast"];

/** Run a copy/save action, toasting its outcome; `false` from the action means the user cancelled. */
export function runImageAction(showGlobalToast: Toast, action: () => Promise<unknown>, success: string | null, failure: string): void {
	void action().then(
		(done) => {
			if (success && done !== false) showGlobalToast(success);
		},
		(error: unknown) => showGlobalToast(failure, error instanceof Error ? error.message : undefined, "error"),
	);
}
