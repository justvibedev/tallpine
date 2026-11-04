import { useTranslation } from "react-i18next";
import { SidebarContent, SidebarGroup, SidebarGroupContent, SidebarMenu, SidebarMenuItem } from "../ui/sidebar";
import { SettingsSaveStatus, useSettingsPage } from "../SettingsDialog";
import { SidebarTopNavRow } from "./SidebarTopNavRow";

export function SettingsSidebarNav({ layer }: { layer: NonNullable<ReturnType<typeof useSettingsPage>> }) {
	const { t } = useTranslation();
	return (
		<SidebarContent className="scrollbar-none gap-0 px-2">
			<SidebarGroup className="p-0">
				<SidebarGroupContent>
					<SidebarMenu aria-label={t("settings.navSectionsAria")} className="gap-0.5" role="navigation">
						{layer.navItems.map(({ id, label, icon: Icon, active, disabled, onSelect }) => (
							<SidebarMenuItem key={id}>
								<SidebarTopNavRow active={active} disabled={disabled} icon={<Icon aria-hidden="true" />} label={label} onClick={onSelect} tooltip={label} />
							</SidebarMenuItem>
						))}
					</SidebarMenu>
				</SidebarGroupContent>
				<SettingsSaveStatus />
			</SidebarGroup>
		</SidebarContent>
	);
}
