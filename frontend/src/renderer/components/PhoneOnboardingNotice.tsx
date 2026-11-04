import { Smartphone, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useUiStore } from "../stores/ui-store";
import { TopbarButton } from "./TopbarButton";

const DISMISS_KEY = "tallpine.phoneOnboardingDismissed";

function readDismissed(): boolean {
	try {
		return window.localStorage.getItem(DISMISS_KEY) === "1";
	} catch {
		return false;
	}
}

/** Onboarding offer: pairing a phone is one tap away on the Connections page,
 * which shows the QR for every LAN and Tailscale address (pinned TLS, no admin steps). */
export function PhoneOnboardingNotice() {
	const { t } = useTranslation();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const [dismissed, setDismissed] = useState(readDismissed);
	if (dismissed) return null;
	const dismiss = () => {
		try {
			window.localStorage.setItem(DISMISS_KEY, "1");
		} catch {
			// Storage unavailable: dismiss for this mount only.
		}
		setDismissed(true);
	};
	return (
		<div className="flex w-full justify-center px-3">
			<div className="w-full max-w-[620px] rounded-welcome-panel border border-[var(--color-border-import-modal)] bg-[var(--color-bg-import-card)] px-4 py-4" data-testid="phone-onboarding-notice">
				<div className="flex items-start gap-3">
					<span className="grid size-9 shrink-0 place-items-center rounded-lg bg-[var(--color-bg-import-chip)] text-[var(--color-text-import-muted)]">
						<Smartphone className="size-4" aria-hidden="true" />
					</span>
					<div className="min-w-0 flex-1">
						<p className="text-[14px] font-semibold text-[var(--color-text-import-title)]">{t("startup.phoneSetupTitle")}</p>
						<p className="mt-0.5 text-[12px] leading-5 text-[var(--color-text-import-muted)]">{t("startup.phoneSetupBody")}</p>
						<div className="mt-2">
							<TopbarButton onClick={() => openGlobalSettings("connections")} variant="primary">
								{t("startup.phoneSetupAction")}
							</TopbarButton>
						</div>
					</div>
					<TopbarButton aria-label={t("startup.phoneSetupDismiss")} className="!size-7 shrink-0" onClick={dismiss} variant="icon">
						<X className="size-4" aria-hidden="true" />
					</TopbarButton>
				</div>
			</div>
		</div>
	);
}
