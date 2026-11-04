/** i18next options that pick the "this Mac" wording of a string on macOS and
 * the "this computer" wording elsewhere (keys with a `_mac` variant). Read at
 * call time from the user agent so it needs no module-level platform state. */
export function thisComputerOptions(): { context?: "mac" } {
	const platform = typeof navigator === "undefined" ? "" : `${navigator.platform} ${navigator.userAgent}`;
	return /mac/i.test(platform) && !/iphone|ipad/i.test(platform) ? { context: "mac" } : {};
}
