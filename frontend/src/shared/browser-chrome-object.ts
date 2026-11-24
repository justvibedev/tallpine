/**
 * Gives a Browser panel page the `window.chrome` members real Chrome has.
 *
 * Browser tabs send a Chrome user agent (main/browser-user-agent.ts), but
 * Electron's `window.chrome` is an empty object. Google sign-in treats a
 * "Chrome" without `chrome.app`, `chrome.csi` and `chrome.loadTimes` as an
 * embedded browser and rejects it at the identifier step ("This browser or
 * app may not be secure"). Probed on Electron 44 (Chrome 152): with these
 * members present the identifier step proceeds; without them it is rejected
 * whatever the Sec-CH-UA brands say.
 *
 * Runs in the page's main world through `contextBridge.executeInMainWorld`,
 * which serializes it with toString: it must not reference anything outside
 * its own body. Members the page already has are left alone.
 */
export function installChromeObject(): void {
	if (typeof window === "undefined") return;
	const host = window as unknown as { chrome?: Record<string, unknown> };
	const chrome = host.chrome ?? (host.chrome = {});
	const timing = () => performance.getEntriesByType("navigation")[0] as PerformanceNavigationTiming | undefined;
	const seconds = (offset: number | undefined) => (performance.timeOrigin + (offset ?? 0)) / 1000;
	if (!("loadTimes" in chrome)) {
		chrome.loadTimes = function loadTimes() {
			const nav = timing();
			const protocol = nav?.nextHopProtocol ?? "";
			return {
				requestTime: seconds(nav?.requestStart),
				startLoadTime: seconds(nav?.startTime),
				commitLoadTime: seconds(nav?.responseStart),
				finishDocumentLoadTime: seconds(nav?.domContentLoadedEventEnd),
				finishLoadTime: seconds(nav?.loadEventEnd),
				firstPaintTime: seconds(nav?.responseEnd),
				firstPaintAfterLoadTime: 0,
				navigationType: "Other",
				wasFetchedViaSpdy: protocol === "h2" || protocol === "h3",
				wasNpnNegotiated: protocol === "h2" || protocol === "h3",
				npnNegotiatedProtocol: protocol || "unknown",
				wasAlternateProtocolAvailable: false,
				connectionInfo: protocol || "http/1.1",
			};
		};
	}
	if (!("csi" in chrome)) {
		chrome.csi = function csi() {
			const nav = timing();
			return {
				startE: Math.round(performance.timeOrigin),
				onloadT: Math.round(performance.timeOrigin + (nav?.domContentLoadedEventEnd ?? 0)),
				pageT: performance.now(),
				tran: 15,
			};
		};
	}
	if (!("app" in chrome)) {
		chrome.app = {
			isInstalled: false,
			InstallState: { DISABLED: "disabled", INSTALLED: "installed", NOT_INSTALLED: "not_installed" },
			RunningState: { CANNOT_RUN: "cannot_run", READY_TO_RUN: "ready_to_run", RUNNING: "running" },
			getDetails: function getDetails() {
				return null;
			},
			getIsInstalled: function getIsInstalled() {
				return false;
			},
			installState: function installState(callback?: (state: string) => void) {
				callback?.("not_installed");
			},
			runningState: function runningState() {
				return "cannot_run";
			},
		};
	}
}
