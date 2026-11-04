/**
 * A usage-limit reset time as the owner reads it: a clock time for today, with
 * the weekday and date when the reset is on another day.
 */
export function formatUsageReset(until: string | Date, locale: string | undefined, now: Date = new Date()): string {
	const at = typeof until === "string" ? new Date(until) : until;
	const sameDay = at.toDateString() === now.toDateString();
	return new Intl.DateTimeFormat(locale, sameDay
		? { hour: "numeric", minute: "2-digit" }
		: { weekday: "short", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }).format(at);
}
