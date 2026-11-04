import { useTranslation } from "react-i18next";
import { PanelToggleButton } from "../PanelToggleButton";
import { SidebarHeader } from "../ui/sidebar";
import { NewSessionSplitButton } from "./NewSessionSplitButton";
import type { Selection } from "./types";

/** Header of the Sessions panel: collapse toggle, title, "+ New". Mirrors the right panel's header toggle. On the settings layer only the toggle remains: the panel is not about sessions there. */
export function SessionsPanelHeader({ selection, toggleSidebar, settingsOpen = false }: { selection: Selection; toggleSidebar: () => void; settingsOpen?: boolean }) {
	const { t } = useTranslation();
	return (
		<SidebarHeader className="flex-row items-center gap-1 p-0 px-2 pb-2 pt-2">
			<PanelToggleButton open onClick={toggleSidebar} side="left" testId="sessions-panel-toggle" />
			{settingsOpen ? null : (
				<>
					<h2 className="sidebar-expanded-chrome min-w-0 flex-1 truncate text-sm font-semibold text-foreground">{t("sessionsPanel.title")}</h2>
					<NewSessionSplitButton selection={selection} />
				</>
			)}
		</SidebarHeader>
	);
}
