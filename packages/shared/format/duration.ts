/** Chat status label: whole seconds from "1s", then minutes, then "1h 2m" (no seconds past a minute). */
export function formatDurationStatus(ms: number): string {
	if (ms < 60_000) return `${Math.max(1, Math.floor(ms / 1000))}s`;
	const totalMinutes = Math.floor(ms / 60_000);
	if (totalMinutes < 60) return `${totalMinutes}m`;
	const hours = Math.floor(totalMinutes / 60);
	const minutes = totalMinutes % 60;
	return minutes === 0 ? `${hours}h` : `${hours}h ${minutes}m`;
}

/** Step timer: "38s", "4m 10s", "1h 2m". */
export function formatDurationClock(ms: number): string {
	const total = Math.max(0, Math.round(ms / 1000));
	if (total < 60) return `${total}s`;
	const minutes = Math.floor(total / 60);
	if (minutes < 60) return `${minutes}m ${total % 60}s`;
	return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

/** Locale-numbered span from seconds: "1h 5m", "4m", "9s". */
export function formatDurationLocale(totalSeconds: number, locale: string): string {
	const seconds = Math.max(0, Math.round(totalSeconds));
	const hours = Math.floor(seconds / 3600);
	const minutes = Math.floor((seconds % 3600) / 60);
	const number = new Intl.NumberFormat(locale);
	if (hours > 0) return `${number.format(hours)}h ${number.format(minutes)}m`;
	return minutes > 0 ? `${number.format(minutes)}m` : `${number.format(seconds % 60)}s`;
}

/** Remaining time as its largest unit: "now", "5m", "3h", "4d". */
export function formatResetIn(seconds: number): string {
	if (seconds <= 0) return "now";
	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${Math.max(1, minutes)}m`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h`;
	return `${Math.floor(hours / 24)}d`;
}

/** "5 minutes ago" style text; `justNow` is returned under a minute or for an unparseable time. */
export function formatRelativeLong(timestamp: string, justNow: string, now: number = Date.now(), locale?: string): string {
	const elapsedMinutes = Math.floor((now - Date.parse(timestamp)) / 60_000);
	if (!Number.isFinite(elapsedMinutes) || elapsedMinutes < 1) return justNow;
	const unit: Intl.RelativeTimeFormatUnit = elapsedMinutes < 60 ? "minute" : elapsedMinutes < 1_440 ? "hour" : "day";
	const amount = unit === "minute" ? elapsedMinutes : unit === "hour" ? Math.floor(elapsedMinutes / 60) : Math.floor(elapsedMinutes / 1_440);
	return new Intl.RelativeTimeFormat(locale, { numeric: "always" }).format(-amount, unit);
}
