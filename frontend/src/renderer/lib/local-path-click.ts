import { appI18n } from "../i18n";
import { tallpineBridge } from "./bridge";
import { copyLinkText } from "./copy-link";
import { useUiStore } from "../stores/ui-store";
import { isLocalPathCandidate, type LocalPathAction, type LocalPathResult } from "../../shared/local-path";
import { isTallpineLink } from "./session-links";

/** Toast a failed local-path action: "File not found" as info, anything else as an error. */
export function reportLocalPathResult(result: LocalPathResult): void {
	if (result.ok) return;
	const notFound = result.reason === "not_found";
	useUiStore.getState().showGlobalToast(appI18n.t(notFound ? "path.notFound" : "path.failed"), undefined, notFound ? "info" : "error");
}

/** Run one local-path action in main and toast when it fails. */
export function performLocalPath(path: string, action: LocalPathAction): Promise<void> {
	return tallpineBridge.localPath.perform(path, action).then(reportLocalPathResult, () => undefined);
}

/**
 * A plain click on a local path in chat. An existing file opens in its default
 * app, the menu's Open: main opens only allowlisted document types and reveals
 * anything else (scripts, installers), so a click never executes a file. A
 * directory, a missing path or one main cannot inspect is copied, as before.
 */
export async function openLocalPathOnClick(path: string): Promise<void> {
	const inspection = await tallpineBridge.localPath.inspect(path).catch(() => undefined);
	if (inspection?.ok && inspection.kind === "file") return performLocalPath(path, "open");
	copyLinkText(path);
}

/** A markdown link's href when it is an absolute or home path on this machine, not a URL or in-app link. */
export function linkedLocalPath(href: string | undefined): string | undefined {
	if (!href || isTallpineLink(href)) return undefined;
	let path = href;
	try {
		path = decodeURI(href);
	} catch {
		return undefined;
	}
	return isLocalPathCandidate(path) ? path : undefined;
}
