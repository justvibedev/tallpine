/** URL comparison helpers for blank tabs and same-origin checks. */

export const MAX_CLOSED_TABS = 5;

// Mirrors the main process's isBlankBrowserEntry (browser-view-host.ts):
// a freshly-opened tab reports its URL as the literal string "about:blank"
// once its initial load settles, not an empty string — a plain truthiness
// check on `url` treats that as "real" content worth remembering.
export function isBlankTabUrl(url: string): boolean {
	return !url || url === "about:blank";
}

export function sameBrowserURL(left: string, right: string): boolean {
	try {
		const normalize = (value: string) => {
			const parsed = new URL(value);
			parsed.hostname = parsed.hostname.replace(/^www\./i, "");
			parsed.hash = "";
			return parsed.href;
		};
		return normalize(left) === normalize(right);
	} catch {
		return left === right;
	}
}

export function sameBrowserOrigin(left: string, right: string): boolean {
	try {
		return new URL(left).origin === new URL(right).origin;
	} catch {
		return false;
	}
}
