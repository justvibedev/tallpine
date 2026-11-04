import { useTranslation } from "react-i18next";
import { Search } from "lucide-react";
import { effectiveShortcutBindings, shortcutBindingKeys } from "../../../shared/shortcuts";
import { SidebarMenuItem, useSidebar } from "../ui/sidebar";
import { useKeybindingsStore } from "../../stores/keybindings-store";
import { SidebarTopNavRow } from "./SidebarTopNavRow";
import { isMac } from "./constants";

export function SidebarSearchButton({ onOpen }: { onOpen: () => void }) {
	const { t } = useTranslation();
	const { state } = useSidebar();
	const isCollapsed = state === "collapsed";
	const overrides = useKeybindingsStore((store) => store.overrides);
	const paletteBinding = effectiveShortcutBindings("command-palette", isMac, overrides)[0];
	const commandPaletteShortcutLabel = paletteBinding
		? shortcutBindingKeys(paletteBinding, isMac).join(isMac ? " " : "+")
		: "Unassigned";
	return (
		<SidebarMenuItem className="group-data-[collapsible=icon]:mb-0">
			<SidebarTopNavRow
				icon={<Search strokeWidth={1.75} aria-hidden="true" />}
				label={t("shell.search")}
				onClick={() => {
					// Open on the microtask after this click rather than inside it: mounting
					// the palette dialog while this button's tooltip layer is still tearing
					// down from the same pointer sequence dismissed it immediately. The
					// "defers opening" test pins the deferral so it is not dropped as noise.
					queueMicrotask(onOpen);
				}}
				tooltip={isCollapsed ? t("shell.search") : undefined}
				trailing={commandPaletteShortcutLabel}
			/>
		</SidebarMenuItem>
	);
}
