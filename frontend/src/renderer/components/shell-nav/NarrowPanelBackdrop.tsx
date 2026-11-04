import { useTranslation } from "react-i18next";
import { useUiStore } from "../../stores/ui-store";

/** Click-away layer under the floating Sessions panel (narrow windows only). */
export function NarrowPanelBackdrop() {
	const { t } = useTranslation();
	const open = useUiStore((state) => state.isNarrowShell && state.isNarrowPanelOpen);
	const close = useUiStore((state) => state.closeNarrowPanel);
	if (!open) return null;
	return (
		<button
			aria-label={t("sessionsPanel.closeOverlay")}
			className="fixed inset-0 z-[calc(var(--z-chrome)-1)] cursor-default bg-transparent"
			data-testid="narrow-panel-backdrop"
			onClick={close}
			tabIndex={-1}
			type="button"
		/>
	);
}
