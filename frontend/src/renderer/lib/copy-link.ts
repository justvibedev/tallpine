import { appI18n } from "../i18n";
import { tallpineBridge } from "./bridge";
import { useUiStore } from "../stores/ui-store";

/** Open modifier for chat links: Cmd (macOS) or Ctrl/Alt elsewhere. A plain click copies instead. */
export function isOpenLinkClick(event: { metaKey: boolean; ctrlKey: boolean; altKey: boolean }): boolean {
	return event.metaKey || event.ctrlKey || event.altKey;
}

/** Copy a link or path from chat and confirm with a brief toast. */
export function copyLinkText(text: string): void {
	void tallpineBridge.clipboard.writeText(text).then(
		() => useUiStore.getState().showGlobalToast(appI18n.t("link.copied"), undefined, "top-center"),
		() => useUiStore.getState().showGlobalToast(appI18n.t("link.copyFailed"), undefined, "error"),
	);
}
