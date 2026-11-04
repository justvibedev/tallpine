// Leaving the scanner after a pairing, without racing the sheet above it.
//
// A new computer is named in a form sheet pushed over the scanner. Saving the
// name pops that sheet, and the scanner used to pop itself moments later while
// the sheet was still animating away. On iOS the second pop can be lost to the
// native stack while JS already considers the scanner gone, which left the
// scanner on screen, unmounted from React, with its "Connecting…" spinner
// frozen. Waiting until the scanner is focused again, and then for the sheet's
// dismissal to finish, makes it the only screen being removed.

export type SettleDeps = {
	isFocused: () => boolean;
	sleep: (ms: number) => Promise<void>;
};

export type SettleOptions = {
	/** How long to wait for the scanner to regain focus before going anyway. */
	focusTimeoutMs?: number;
	/** The sheet's dismissal animation, which outlasts the focus change. */
	settleMs?: number;
	pollMs?: number;
};

/** Resolves once nothing is presented over the scanner and its animation is done. */
export async function waitForSheetToSettle(deps: SettleDeps, options: SettleOptions = {}): Promise<void> {
	const { focusTimeoutMs = 2000, settleMs = 450, pollMs = 50 } = options;
	let waited = 0;
	while (!deps.isFocused() && waited < focusTimeoutMs) {
		await deps.sleep(pollMs);
		waited += pollMs;
	}
	await deps.sleep(settleMs);
}

/** Resolves with the value, or with `fallback` once `ms` has passed. */
export function withTimeout<T>(work: Promise<T>, ms: number, fallback: T): Promise<T> {
	let timer: ReturnType<typeof setTimeout> | undefined;
	const late = new Promise<T>((resolve) => { timer = setTimeout(() => resolve(fallback), ms); });
	return Promise.race([work, late]).finally(() => clearTimeout(timer));
}
