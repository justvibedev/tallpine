import { useEffect, useState } from "react";
import { KeyRound, X } from "lucide-react";
import { useUiStore } from "../../stores/ui-store";
import { Button } from "../ui/button";
import type { BrowserPanelViewState } from "./browser-types";

/**
 * Shown while the page waits on a passkey request. Electron has no WebAuthn
 * UI for iCloud Keychain, phone (QR) or password-manager passkeys, so without
 * this the page just sits on "Complete sign-in using your passkey".
 */
export function BrowserPasskeyNotice({ v }: { v: BrowserPanelViewState }) {
	const { navState, openCurrentPageExternally, t } = v;
	const pending = navState.passkeyPending === true;
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const [dismissed, setDismissed] = useState(false);
	// A dismissal covers one request; the next one shows the notice again.
	useEffect(() => {
		if (!pending) setDismissed(false);
	}, [pending]);
	if (!pending || dismissed) return null;
	return (
		<div
			className="flex shrink-0 items-start gap-2 border-y border-border bg-background px-2.5 py-2 text-xs text-foreground"
			role="status"
			data-testid="browser-passkey-notice"
		>
			<KeyRound aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
			<p className="m-0 min-w-0 flex-1 leading-snug">{t("browser.passkey.message")}</p>
			<Button type="button" size="sm" variant="outline" className="shrink-0" onClick={() => openGlobalSettings("browserProfiles")}>
				{t("browser.passkey.importSignIn")}
			</Button>
			<Button
				type="button"
				size="sm"
				variant="outline"
				className="shrink-0"
				onClick={() => void openCurrentPageExternally(navState.passkeyExternalUrl ?? navState.url)}
			>
				{t("browser.passkey.openExternal")}
			</Button>
			<Button
				type="button"
				size="icon-sm"
				variant="ghost"
				className="shrink-0"
				aria-label={t("browser.passkey.dismiss")}
				onClick={() => setDismissed(true)}
			>
				<X aria-hidden="true" />
			</Button>
		</div>
	);
}
