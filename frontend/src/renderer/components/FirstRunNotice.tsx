import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { tallpineBridge } from "../lib/bridge";
import { useFirstRunNoticeStore } from "../stores/first-run-notice-store";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogOverlay,
	DialogTitle,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

export const TERMS_URL = "https://tallpine.eu/terms.html";

/** One-time notice on first launch; skipped once acknowledged or pre-accepted by managed config. */
export function FirstRunNotice() {
	const { t } = useTranslation();
	const { loaded, accepted, load, accept } = useFirstRunNoticeStore();
	useEffect(() => {
		void load();
	}, [load]);
	if (!loaded || accepted) return null;
	return (
		// Not dismissable by Escape or outside click: the only exit is the acknowledgement.
		<Dialog open>
			<DialogContent
				showCloseButton={false}
				className={`${settingsDialogContentClass} w-[min(440px,calc(100vw-24px))]`}
				overlay={<DialogOverlay />}
				onEscapeKeyDown={(event) => event.preventDefault()}
				onPointerDownOutside={(event) => event.preventDefault()}
				onInteractOutside={(event) => event.preventDefault()}
			>
				<div className={`${settingsDialogHeaderClass} border-b-0 p-5`}>
					<DialogTitle className="settings-dialog-title text-base">{t("firstRun.title")}</DialogTitle>
					<DialogDescription className="text-control leading-5 text-settings-muted">
						{t("firstRun.body")}{" "}
						<a
							href={TERMS_URL}
							className="underline"
							onClick={(event) => {
								event.preventDefault();
								void tallpineBridge.app.openExternal(TERMS_URL);
							}}
						>
							{t("firstRun.terms")}
						</a>
					</DialogDescription>
				</div>
				<div className={`${settingsDialogFooterClass} gap-2 border-t-0 p-4 pt-1`}>
					<Button type="button" variant="footer-primary" className="h-8 rounded-[10px] px-4 text-sm" onClick={() => void accept()}>
						{t("firstRun.accept")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
