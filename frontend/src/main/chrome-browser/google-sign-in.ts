/**
 * Google refuses sign-in in a Chrome under agent control (B854), so a person
 * who meets Google sign-in in Real Chrome mode must end up in plain Chrome on
 * the same profile. Two ways lead there without the person finding the
 * Browser panel's Sign in to sites button:
 *  - an agent's handoff on a Google sign-in page opens plain Chrome at the
 *    page the sign-in continues to, and the handoff ends when it closes;
 *  - an agent tab that reaches Google's refusal ("Couldn't sign you in") means
 *    the person just tried in the agent's window: plain Chrome opens at once.
 */
import type { BrowserHandoffPage } from "../browser-handoff";
import { googleSignInStartURL, isGoogleSignInRejected } from "../../shared/chrome-mode";
import type { ChromeHostProcess } from "./host";

export class ChromeGoogleSignIn {
	private starting: Promise<void> | null = null;

	constructor(
		private readonly process: ChromeHostProcess,
		private readonly log: (message: string) => void,
	) {}

	/** An agent tab now shows url. */
	tabNavigated(url: string): void {
		if (!isGoogleSignInRejected(url) || this.process.signingIn || this.starting) return;
		this.log("Google refused sign-in in the agent's Chrome; reopening it without agent control");
		this.starting = this.process
			.signIn(googleSignInStartURL(url))
			.catch((error: unknown) => this.log(`Opening Chrome for signing in failed: ${error instanceof Error ? error.message : String(error)}`))
			.finally(() => {
				this.starting = null;
			});
	}

	/** The handoff page for an agent tab on Google sign-in at url: the person works in plain Chrome. */
	async handoffPage(url: string): Promise<BrowserHandoffPage> {
		await this.process.signIn(googleSignInStartURL(url));
		let off = () => {};
		const ended = new Promise<void>((resolve) => {
			if (!this.process.signingIn) return resolve();
			off = this.process.onChange(() => {
				if (this.process.signingIn) return;
				off();
				resolve();
			});
		});
		return {
			signIn: true,
			// The agent's tab is gone while plain Chrome owns the profile.
			url: () => undefined,
			ended,
			release: async () => {
				off();
				// Continue, a timeout or a cancel also ends the sign-in, so the agent's Chrome comes back.
				if (this.process.signingIn) await this.process.finishSignIn();
			},
		};
	}
}
