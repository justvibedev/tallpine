import { useState } from "react";
import { Copy, ExternalLink, FolderSearch } from "lucide-react";
import { useTranslation } from "react-i18next";
import { tallpineBridge } from "../lib/bridge";
import { isMacPlatform, isWindowsPlatform } from "../lib/platform";
import { performLocalPath } from "../lib/local-path-click";
import type { LocalPathAction, LocalPathInspection } from "../../shared/local-path";
import { ContextMenuItem } from "./ui/context-menu";

/** Inspect-on-open state plus the toast-on-missing action runner shared by every local-path menu. */
export function useLocalPathMenu(path: string) {
	const [inspection, setInspection] = useState<LocalPathInspection>();
	const perform = (action: LocalPathAction) => void performLocalPath(path, action);
	const onOpenChange = (open: boolean) => {
		if (open) void tallpineBridge.localPath.inspect(path).then(setInspection, () => setInspection({ ok: false, reason: "invalid" }));
	};
	return { inspection, perform, onOpenChange };
}

function revealKey() {
	return isMacPlatform() ? "path.reveal" : isWindowsPlatform() ? "path.revealWindows" : "path.revealLinux";
}

/** Reveal / Open (allowlisted document types only) / Copy path. */
export function LocalPathMenuItems({ menu }: { menu: ReturnType<typeof useLocalPathMenu> }) {
	const { t } = useTranslation();
	// Not-found stays actionable so the click surfaces the "File not found" toast instead of a dead menu.
	const available = menu.inspection?.ok === true || (menu.inspection?.ok === false && menu.inspection.reason === "not_found");
	const canOpen = menu.inspection?.ok === true && menu.inspection.canOpen;
	return (
		<>
			<ContextMenuItem disabled={!available} onSelect={() => menu.perform("reveal")}>
				<FolderSearch aria-hidden="true" />
				{t(revealKey())}
			</ContextMenuItem>
			<ContextMenuItem disabled={!canOpen} onSelect={() => menu.perform("open")}>
				<ExternalLink aria-hidden="true" />
				{t("path.open")}
			</ContextMenuItem>
			<ContextMenuItem disabled={!available} onSelect={() => menu.perform("copy")}>
				<Copy aria-hidden="true" />
				{t("path.copy")}
			</ContextMenuItem>
		</>
	);
}
