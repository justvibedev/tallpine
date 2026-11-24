import { useState } from "react";
import { SlidersHorizontal } from "lucide-react";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { sitePermissionOrigin } from "../../../shared/browser-site-permissions";
import type { BrowserPanelViewState } from "./browser-types";
import { BrowserControlTooltip } from "./BrowserTabs";
import { sitePermissionKindLabel } from "./BrowserSitePermissionBar";

/**
 * Toolbar menu listing what the person allowed or blocked for this page's
 * site, with a reset per permission. Shown only once something is remembered.
 */
export function BrowserSiteSettingsMenu({ v }: { v: BrowserPanelViewState }) {
	const { navState, t, viewId } = v;
	const [open, setOpen] = useState(false);
	const remembered = navState.sitePermissions ?? [];
	const origin = sitePermissionOrigin(navState.url);
	if (!remembered.length || !origin || !viewId) return null;
	const site = new URL(origin).host;
	const reset = (kind?: (typeof remembered)[number]["kind"]) =>
		void window.tallpine?.browser.resetSitePermission(kind ? { viewId, kind } : { viewId });
	return (
		<DropdownMenu onOpenChange={setOpen} open={open}>
			<BrowserControlTooltip disabled={open} label={t("browser.sitePermissions.settings")}>
				<DropdownMenuTrigger asChild>
					<Button aria-label={t("browser.sitePermissions.settings")} size="icon-sm" type="button" variant="ghost">
						<SlidersHorizontal aria-hidden="true" className="size-icon-base" />
					</Button>
				</DropdownMenuTrigger>
			</BrowserControlTooltip>
			<DropdownMenuContent align="end" className="w-64" data-browser-native-overlay="true">
				<p className="truncate px-2 py-1.5 text-xs font-semibold">{t("browser.sitePermissions.settingsFor", { site })}</p>
				<div className="my-1 h-px bg-border" role="separator" />
				{remembered.map(({ kind, decision }) => (
					<DropdownMenuItem className="gap-1.5" key={kind} onSelect={() => reset(kind)}>
						<span className="flex-1 truncate">{sitePermissionKindLabel(t, kind)}</span>
						<span className="shrink-0 text-caption text-passive">
							{decision === "allow" ? t("browser.sitePermissions.allowed") : t("browser.sitePermissions.blocked")}
						</span>
						<span className="shrink-0 text-caption text-accent">{t("browser.sitePermissions.reset")}</span>
					</DropdownMenuItem>
				))}
				{remembered.length > 1 ? (
					<>
						<div className="my-1 h-px bg-border" role="separator" />
						<DropdownMenuItem onSelect={() => reset()}>{t("browser.sitePermissions.resetAll")}</DropdownMenuItem>
					</>
				) : null}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
