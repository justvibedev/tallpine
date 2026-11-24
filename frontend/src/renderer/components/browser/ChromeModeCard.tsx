import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { CHROME_INSTALL_URL, isGoogleSignInPage, type ChromeSessionState } from "../../../shared/chrome-mode";
import { tallpineBridge } from "../../lib/bridge";
import { Button } from "../ui/button";

const idle: ChromeSessionState = { running: false, activeTab: null };

/**
 * The Browser panel of a session whose project browses in Google Chrome
 * (Real Chrome mode, B849 q4): no page here, the session's Chrome tab and a
 * way to bring its window forward.
 */
export function ChromeModeCard({ sessionId }: { sessionId: string }) {
	const { t } = useTranslation();
	const [state, setState] = useState<ChromeSessionState>(idle);
	useEffect(() => {
		let live = true;
		setState(idle);
		void tallpineBridge.browser.chromeState(sessionId).then((next) => live && setState(next)).catch(() => undefined);
		const off = tallpineBridge.browser.onChromeStateChanged((change) => {
			if (change.sessionId === sessionId) setState(change.state);
		});
		return () => {
			live = false;
			off();
		};
	}, [sessionId]);
	const tab = state.activeTab;
	const error = state.error;
	// The agent's tab is on Google sign-in, which fails in this window: point at the way that works.
	const googleSignIn = tab ? isGoogleSignInPage(tab.url) : false;
	// Google refuses sign-in in a Chrome under agent control (B854): the person
	// signs in in plain Chrome on the same profile, then the agent's Chrome returns.
	const [pending, setPending] = useState(false);
	const run = (request: (id: string) => Promise<ChromeSessionState>) => {
		setPending(true);
		void request(sessionId)
			.then(setState)
			.catch(() => undefined)
			.finally(() => setPending(false));
	};
	if (state.signingIn) {
		return (
			<div className="flex h-full min-h-0 flex-col items-center justify-center gap-3 px-6 text-center" data-testid="browser-chrome-mode" role="status">
				<h2 className="m-0 text-heading-sm font-semibold text-foreground">{t("browser.chromeMode.signingInTitle")}</h2>
				<p className="m-0 max-w-md text-sm text-muted-foreground">{t("browser.chromeMode.signingInDescription")}</p>
				<Button type="button" size="sm" disabled={pending} onClick={() => run(tallpineBridge.browser.chromeSignInDone)}>
					{t("browser.chromeMode.signInDone")}
				</Button>
			</div>
		);
	}
	return (
		<div className="flex h-full min-h-0 flex-col items-center justify-center gap-3 px-6 text-center" data-testid="browser-chrome-mode" role="status">
			<h2 className="m-0 text-heading-sm font-semibold text-foreground">{t("browser.chromeMode.title")}</h2>
			<p className="m-0 max-w-md text-sm text-muted-foreground">{t("browser.chromeMode.description")}</p>
			{tab ? (
				<div className="flex max-w-md min-w-0 flex-col gap-0.5" data-testid="browser-chrome-mode-tab">
					<span className="truncate text-sm text-foreground">{tab.title || t("browser.chromeMode.untitled")}</span>
					<span className="truncate font-mono text-xs text-passive">{tab.url}</span>
				</div>
			) : (
				<p className="m-0 text-sm text-passive">{t("browser.chromeMode.noTab")}</p>
			)}
			{googleSignIn ? (
				<p className="m-0 max-w-md text-sm font-medium text-status-needs-you" data-testid="browser-chrome-mode-google-sign-in">
					{t("browser.chromeMode.googleSignIn")}
				</p>
			) : null}
			{error ? (
				<p className="m-0 max-w-md text-sm text-error" role="alert">
					{error.code === "BROWSER_CHROME_NOT_FOUND" ? t("browser.chromeMode.notFound") : error.code === "BROWSER_CHROME_PROFILE_IN_USE" ? t("browser.chromeMode.profileInUse") : error.message}
				</p>
			) : null}
			{error?.code === "BROWSER_CHROME_NOT_FOUND" ? (
				<Button type="button" size="sm" variant="outline" onClick={() => void tallpineBridge.app.openExternal(CHROME_INSTALL_URL)}>
					{t("browser.chromeMode.install")}
				</Button>
			) : (
				<div className="flex flex-wrap items-center justify-center gap-2">
					<Button type="button" size="sm" variant="outline" disabled={!tab} onClick={() => void tallpineBridge.browser.chromeShow(sessionId)}>
						{t("browser.chromeMode.show")}
					</Button>
					<Button type="button" size="sm" variant={googleSignIn ? "primary" : "outline"} disabled={pending} onClick={() => run(tallpineBridge.browser.chromeSignIn)}>
						{t("browser.chromeMode.signIn")}
					</Button>
				</div>
			)}
			{error?.code === "BROWSER_CHROME_NOT_FOUND" ? null : <p className="m-0 max-w-md text-xs text-passive">{t("browser.chromeMode.signInHint")}</p>}
		</div>
	);
}
