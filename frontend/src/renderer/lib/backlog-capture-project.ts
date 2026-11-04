const KEY = "ao.backlog.captureProject";

function storage(): Storage | null {
	return typeof window === "undefined" ? null : window.localStorage ?? null;
}

/** The project the user last added a backlog item to; the capture popover starts there when none is in view. */
export function readCaptureProject(): string | undefined {
	try {
		return storage()?.getItem(KEY) ?? undefined;
	} catch {
		return undefined;
	}
}

export function rememberCaptureProject(projectId: string): void {
	try {
		storage()?.setItem(KEY, projectId);
	} catch {
		// A private or full store only loses the convenience.
	}
}
