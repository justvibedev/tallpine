/** Real Chrome mode (B849) values shared by Electron main and the renderer. */

/** Where a person without Google Chrome installs it (no download by Tallpine, q3). */
export const CHROME_INSTALL_URL = "https://www.google.com/chrome/";

/** project.browserBackend value that runs agent browsing in Google Chrome. */
export const BROWSER_BACKEND_CHROME = "chrome";

/** Error code of an agent command refused while the person signs in (B854). */
export const CHROME_SIGNING_IN = "BROWSER_CHROME_SIGNING_IN";

/**
 * Google's "Couldn't sign you in — This browser or app may not be secure"
 * page, shown to a Chrome under agent control (B854).
 */
export function isGoogleSignInRejected(rawURL: string): boolean {
	try {
		const url = new URL(rawURL);
		return url.hostname === "accounts.google.com" && /\/signin\/rejected\b/.test(url.pathname);
	} catch {
		return false;
	}
}

/**
 * Any page of Google's sign-in flow (account chooser, identifier, password,
 * the refusal). Signing in there fails in a Chrome under agent control, so a
 * person who meets one must sign in in plain Chrome instead.
 */
export function isGoogleSignInPage(rawURL: string): boolean {
	try {
		const url = new URL(rawURL);
		return url.hostname === "accounts.google.com" && /\/(signin|ServiceLogin|AccountChooser|InteractiveLogin)\b/i.test(url.pathname);
	} catch {
		return false;
	}
}

/**
 * Where plain Chrome opens for a Google sign-in page: the site the sign-in
 * continues to (Google then asks for the account there), or undefined to use
 * Google's own sign-in page.
 */
export function googleSignInStartURL(rawURL: string): string | undefined {
	if (!isGoogleSignInPage(rawURL)) return undefined;
	const target = new URL(rawURL).searchParams.get("continue") ?? "";
	try {
		return new URL(target).protocol === "https:" ? target : undefined;
	} catch {
		return undefined;
	}
}

/** What the agent hears when its Chrome tab is on a Google sign-in page. */
export const GOOGLE_SIGN_IN_HINT =
	'This is Google sign-in, which Google refuses while Chrome is under agent control. Never ask the person to sign in in this window. Run `tallpine browser handoff --reason "Sign in to Google"`: Tallpine opens this page in Chrome without agent control, the person signs in and clicks Done, and the command returns once your Chrome is back with the sign-in.';

/** What the Browser panel's Chrome mode card shows for one session (q4). */
export type ChromeSessionState = {
	running: boolean;
	/** The profile is open in plain Chrome for signing in by hand (B854). */
	signingIn?: boolean;
	activeTab: { title: string; url: string } | null;
	error?: { code: string; message: string };
};
