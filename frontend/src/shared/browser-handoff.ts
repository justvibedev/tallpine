/**
 * Browser handoff (B858): an agent pauses while the person does a step only
 * they can do (2FA, CAPTCHA, login). Shared by Electron main, which runs the
 * wait, and the renderer, which shows the banner.
 */

/** One running handoff, as the Browser panel's banner shows it. */
export type BrowserHandoffState = {
	id: string;
	sessionId: string;
	reason: string;
	untilUrl?: string;
	/** Epoch milliseconds when the agent stops waiting. */
	deadline: number;
	/** The person signs in to Google in plain Chrome, not in the agent's window (B854). */
	signIn?: boolean;
};

export type BrowserHandoffChange = { sessionId: string; state: BrowserHandoffState | null };

export const BROWSER_HANDOFF_STATE_CHANNEL = "browser:handoffState";
export const BROWSER_HANDOFF_CONTINUE_CHANNEL = "browser:handoffContinue";
export const BROWSER_HANDOFF_CHANGED_CHANNEL = "browser:handoffChanged";

/**
 * Compiles a URL glob matched against the whole URL: `*` matches any run of
 * characters, `?` exactly one; everything else is literal.
 */
export function compileUrlGlob(glob: string): (url: string) => boolean {
	let source = "";
	for (const ch of glob) {
		if (ch === "*") source += ".*";
		else if (ch === "?") source += ".";
		else source += ch.replace(/[.+^${}()|[\]\\/-]/g, "\\$&");
	}
	const pattern = new RegExp(`^${source}$`, "s");
	return (url) => pattern.test(url);
}
