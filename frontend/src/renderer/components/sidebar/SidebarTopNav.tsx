import { SidebarGroup, SidebarGroupContent, SidebarMenu } from "../ui/sidebar";
import { SidebarSearchButton } from "./SidebarSearchButton";

/** The panel's command-palette entry. Page navigation (Automations, Artifacts, ...) lives on the icon rail. */
export function SidebarTopNav({ commandPaletteEnabled, setCommandPaletteOpen }: { commandPaletteEnabled: boolean; setCommandPaletteOpen: (open: boolean) => void }) {
	if (!commandPaletteEnabled) return null;
	return (
		<SidebarGroup className="p-0 pb-3 group-data-[collapsible=icon]:pb-1">
			<SidebarGroupContent>
				<SidebarMenu className="gap-0.5 group-data-[collapsible=icon]:gap-1">
					<SidebarSearchButton onOpen={() => setCommandPaletteOpen(true)} />
				</SidebarMenu>
			</SidebarGroupContent>
		</SidebarGroup>
	);
}
