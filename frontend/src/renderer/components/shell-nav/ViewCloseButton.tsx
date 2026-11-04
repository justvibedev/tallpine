import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useLeaveView } from "../../lib/leave-view";

/** Top-right close control shared by every full-screen view; see useLeaveView. */
export function ViewCloseButton() {
	const { t } = useTranslation();
	const leave = useLeaveView();
	return (
		<button
			aria-label={t("shell.closeView")}
			className="absolute right-1.5 top-2 z-20 grid size-control-board place-items-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-2 [-webkit-app-region:no-drag]"
			data-testid="view-close"
			onClick={leave}
			title={t("shell.closeView")}
			type="button"
		>
			<X aria-hidden="true" className="size-icon-md" />
		</button>
	);
}
