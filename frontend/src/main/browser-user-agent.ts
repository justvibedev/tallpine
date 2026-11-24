// Electron's default user agent carries the app token ("Tallpine/1.2.3") and
// "Electron/x". Google sign-in and similar checks refuse such embedded browsers,
// so browser partitions present as the plain Chrome the app is built on.
// Electron sends no Sec-CH-UA client hints at all (checked on Electron 44 over
// HTTPS), while real Chrome always does, so they are added back here with
// the same brands navigator.userAgentData reports in the page (Electron 44
// probe: Not?A_Brand 24 + Chromium <major>, platform macOS), so header and JS
// views agree. Only the headers are rewritten; the JS side is Chromium's own,
// except window.chrome, which the tab preload fills in so the page agrees
// with the Chrome UA (shared/browser-chrome-object.ts).

const APP_OR_ELECTRON_TOKEN = /\s(?:Electron|[A-Za-z][\w.-]*)\/\S+/g;

/** Drops the Electron and app-name tokens, keeping Mozilla/AppleWebKit/Chrome/Safari. */
export function chromeUserAgent(userAgent: string): string {
	return userAgent.replace(APP_OR_ELECTRON_TOKEN, (token) => {
		const name = token.trim().split("/")[0];
		return ["Mozilla", "AppleWebKit", "Chrome", "Safari", "Version", "Mobile"].includes(name) ? token : "";
	});
}

type HeaderSession = {
	webRequest: {
		onBeforeSendHeaders(
			filter: { urls: string[] },
			listener: (
				details: { requestHeaders: Record<string, string> },
				callback: (response: { requestHeaders: Record<string, string> }) => void,
			) => void,
		): void;
	};
};
type UserAgentSession = Partial<HeaderSession> & {
	getUserAgent(): string;
	setUserAgent(userAgent: string, acceptLanguages?: string): void;
};

const PLATFORMS: Record<string, string> = { darwin: "macOS", win32: "Windows", linux: "Linux" };

/** The three low-entropy hints Chrome sends on every HTTPS request. */
export function chromeClientHints(chromeVersion: string, platform: string): Record<string, string> {
	const major = chromeVersion.split(".")[0] || "0";
	return {
		"Sec-CH-UA": `"Not?A_Brand";v="24", "Chromium";v="${major}"`,
		"Sec-CH-UA-Mobile": "?0",
		"Sec-CH-UA-Platform": `"${PLATFORMS[platform] ?? "Unknown"}"`,
	};
}

// Electron reuses one Session per partition, so each is configured once.
const configured = new WeakSet<object>();

export function applyChromeUserAgent(session: UserAgentSession | undefined): void {
	if (!session?.getUserAgent || !session.setUserAgent || configured.has(session)) return;
	configured.add(session);
	const userAgent = chromeUserAgent(session.getUserAgent());
	session.setUserAgent(userAgent);
	const version = /Chrome\/([\d.]+)/.exec(userAgent)?.[1] ?? process.versions.chrome ?? "";
	const hints = chromeClientHints(version, process.platform);
	session.webRequest?.onBeforeSendHeaders({ urls: ["https://*/*"] }, (details, callback) => {
		callback({ requestHeaders: { ...details.requestHeaders, ...hints } });
	});
}

type ContentsUserAgent = { getUserAgent(): string; setUserAgent(userAgent: string): void };

/**
 * Session.setUserAgent does not affect WebContents that already exist, and
 * Electron copies the session UA into each WebContents at creation. Set the
 * cleaned UA on the tab (and adopted popup) itself before it loads anything.
 */
export function applyChromeUserAgentToContents(contents: Partial<ContentsUserAgent> | undefined): void {
	if (!contents?.getUserAgent || !contents.setUserAgent) return;
	contents.setUserAgent(chromeUserAgent(contents.getUserAgent()));
}
