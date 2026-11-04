const UNITS = ["B", "KB", "MB", "GB", "TB"] as const;

/** 1024-based size: whole bytes, one decimal under 10, else whole numbers, up to TB. Non-finite or negative reads "0 B". */
/** `wholeKb` keeps KB as whole numbers ("2 KB", never "2.0 KB"), the output of the older per-screen copies. */
export function formatBytes(bytes: number, opts: { wholeKb?: boolean } = {}): string {
	let value = Number.isFinite(bytes) ? Math.max(0, bytes) : 0;
	let unit = 0;
	while (value >= 1024 && unit < UNITS.length - 1) {
		value /= 1024;
		unit += 1;
	}
	return `${unit === 0 || (opts.wholeKb && unit === 1) || value >= 10 ? Math.round(value) : value.toFixed(1)} ${UNITS[unit]}`;
}
