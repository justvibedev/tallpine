import { useTranslation } from "react-i18next";
import { ArrowLeft } from "lucide-react";
import { SidebarFooter } from "../ui/sidebar";
import { AwakeControl } from "../AwakeControl";
import { HealthBadge } from "../HealthBadge";
import { UpdateReadyButton } from "../UpdateReadyButton";
import { useSettingsPage } from "../SettingsDialog";
import { NavRowHighlight } from "../NavRowHighlight";
import { FOOTER_NAV_BUTTON_CLASS } from "./constants";

export function SidebarFooterNav({ daemonStatus, settingsPage, isCollapsed }: { daemonStatus: { state: string } | null; settingsPage: ReturnType<typeof useSettingsPage>; isCollapsed: boolean }) {
	const { t } = useTranslation();
	return (
		<>
		{/* Footer — health and Stay Awake. Settings and Connect mobile live at the
		    bottom of the icon rail. Bottom spacing stays inside the footer so
		    there is no empty strip beneath the final control. */}
		<SidebarFooter
			className="relative mt-auto gap-0 overflow-hidden border-t border-border-strong px-2 !py-2 transition-[padding] duration-200 ease-linear group-data-[collapsible=icon]:min-h-20 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:border-t-0 group-data-[collapsible=icon]:overflow-visible group-data-[collapsible=icon]:px-1.5 group-data-[collapsible=icon]:!pb-2 group-data-[collapsible=icon]:!pt-1.5"
		>
			{/* Always-present daemon status mirror for the smoke suite: no visible
			    daemon-state copy is guaranteed to be mounted elsewhere. */}
			{daemonStatus && (
				<span aria-hidden="true" className="sr-only" data-testid="daemon-status" data-state={daemonStatus.state}>
					daemon {daemonStatus.state}
				</span>
			)}
			{settingsPage ? (
				<>
					<button
						className={FOOTER_NAV_BUTTON_CLASS}
						disabled={settingsPage.cueBusy}
						onClick={settingsPage.close}
						type="button"
					>
						<NavRowHighlight />
						<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2.5 [&_svg]:size-icon-md [&_svg]:shrink-0">
							<ArrowLeft aria-hidden="true" />
							<span className="tracking-tight">{t("settings.back")}</span>
						</span>
					</button>
				</>
			) : (
			<>
			<div
				aria-hidden={isCollapsed || undefined}
				// aria-hidden and tabIndex -1 keep the controls out of reach while the
				// panel is closed; it is not display: none, so the footer border stays
				// until the sliding panel is gone.
				className="sidebar-expanded-chrome relative flex w-full min-w-46.5 items-center justify-start gap-1"
			>
				<HealthBadge tabIndex={isCollapsed ? -1 : 0} />
				<AwakeControl tabIndex={isCollapsed ? -1 : 0} />
				<UpdateReadyButton tabIndex={isCollapsed ? -1 : 0} />
			</div>
			</>
			)}
		</SidebarFooter>
		</>
	);
}
