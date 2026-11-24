import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Hand } from "lucide-react";
import { useBrowserHandoff } from "../../hooks/useBrowserHandoff";
import { tallpineBridge } from "../../lib/bridge";
import { Button } from "../ui/button";

/**
 * Shown while the session's agent waits on the person (`tallpine browser
 * handoff`, B858): what to do, and Continue to hand the browser back.
 */
export function BrowserHandoffNotice({ sessionId, chrome = false }: { sessionId: string; chrome?: boolean }) {
	const { t } = useTranslation();
	const handoff = useBrowserHandoff(sessionId);
	const [pendingId, setPendingId] = useState<string | null>(null);
	if (!handoff) return null;
	const pending = pendingId === handoff.id;
	return (
		<div
			className="flex shrink-0 items-start gap-2 border-y border-border bg-background px-2.5 py-2 text-xs text-foreground"
			role="alert"
			data-testid="browser-handoff-notice"
		>
			<Hand aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-status-needs-you-dot" />
			<div className="flex min-w-0 flex-1 flex-col gap-0.5 text-left">
				<p className="m-0 font-medium text-status-needs-you">{t("browser.handoff.title")}</p>
				<p className="m-0 break-words leading-snug" data-testid="browser-handoff-reason">{handoff.reason}</p>
				<p className="m-0 leading-snug text-muted-foreground">
					{t(handoff.signIn ? "browser.handoff.hintSignIn" : chrome ? "browser.handoff.hintChrome" : "browser.handoff.hint")}
				</p>
			</div>
			<Button
				type="button"
				size="sm"
				className="shrink-0"
				disabled={pending}
				onClick={() => {
					setPendingId(handoff.id);
					void tallpineBridge.browser
						.handoffContinue(sessionId, handoff.id)
						.catch(() => undefined)
						.finally(() => setPendingId(null));
				}}
			>
				{t("browser.handoff.continue")}
			</Button>
		</div>
	);
}
