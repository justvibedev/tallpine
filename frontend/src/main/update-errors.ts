/**
 * Failures that say nothing about this install: the release feed does not
 * exist yet or has no matching asset (404, "no published versions"), the Mac is
 * offline, or GitHub is rate limiting. The updater stays quiet on these and
 * tries again on the next scheduled check.
 */
const QUIET_PATTERNS = [
	/\b404\b/,
	/\b403\b/,
	/\b429\b/,
	/HttpError: 5\d\d/,
	/No published versions/i,
	/Unable to find latest version/i,
	/Cannot find (?:channel|latest)[^\s]*\.yml/i,
	/ERR_UPDATER_LATEST_VERSION_NOT_FOUND/,
	/ERR_UPDATER_CHANNEL_FILE_NOT_FOUND/,
	/ERR_UPDATER_NO_PUBLISHED_VERSIONS/,
	/net::ERR_/,
	/\b(?:ENOTFOUND|EAI_AGAIN|ECONNREFUSED|ECONNRESET|ETIMEDOUT|ENETUNREACH|EHOSTUNREACH)\b/,
	/socket hang up/i,
];

export function isQuietUpdateError(error: unknown): boolean {
	const e = error as { code?: unknown; message?: unknown } | null;
	const text = [typeof e?.code === "string" ? e.code : "", typeof e?.message === "string" ? e.message : String(error)].join(" ");
	return QUIET_PATTERNS.some((pattern) => pattern.test(text));
}
