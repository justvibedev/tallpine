const UNITS: ReadonlyArray<readonly [Intl.RelativeTimeFormatUnit, number]> = [
	["year", 365 * 24 * 60 * 60 * 1000],
	["month", 30 * 24 * 60 * 60 * 1000],
	["week", 7 * 24 * 60 * 60 * 1000],
	["day", 24 * 60 * 60 * 1000],
	["hour", 60 * 60 * 1000],
	["minute", 60 * 1000],
];

/** "2 days ago" style age of a timestamp, in the app locale. */
export function formatBacklogAge(timestamp: string, locale: string | undefined, now: number = Date.now()): string {
	const elapsed = Math.max(0, now - Date.parse(timestamp));
	const format = new Intl.RelativeTimeFormat(locale, { numeric: "auto", style: "short" });
	for (const [unit, size] of UNITS) {
		if (elapsed >= size) return format.format(-Math.floor(elapsed / size), unit);
	}
	return format.format(0, "minute");
}
