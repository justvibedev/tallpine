import { ExternalLink, ShieldQuestion } from "lucide-react";
import type { TFunction } from "i18next";
import type { SitePermissionKind } from "../../../shared/browser-site-permissions";
import { Button } from "../ui/button";
import type { BrowserPanelViewState } from "./browser-types";

export function sitePermissionKindLabel(t: TFunction, kind: SitePermissionKind): string {
	switch (kind) {
		case "camera":
			return t("browser.sitePermissions.kind.camera");
		case "microphone":
			return t("browser.sitePermissions.kind.microphone");
		case "notifications":
			return t("browser.sitePermissions.kind.notifications");
		case "clipboard-read":
			return t("browser.sitePermissions.kind.clipboardRead");
	}
}

function hostOf(origin: string): string {
	try {
		return new URL(origin).host;
	} catch {
		return origin;
	}
}

/**
 * Chrome-style bar for a site permission the page in a person's own tab is
 * waiting on (main/browser-view-host/site-permissions.ts). Allow and Block are
 * remembered for the origin; a mailto:/tel: link is a one-time confirm.
 */
export function BrowserSitePermissionBar({ v }: { v: BrowserPanelViewState }) {
	const { navState, t, viewId } = v;
	const prompt = navState.sitePermissionPrompt;
	if (!prompt || !viewId) return null;
	const respond = (allow: boolean) =>
		void window.tallpine?.browser.respondSitePermission({ viewId, promptId: prompt.id, allow });
	const external = prompt.externalUrl;
	const site = hostOf(prompt.origin) || t("browser.sitePermissions.thisPage");
	const message = external
		? t("browser.sitePermissions.openExternal", { site, url: external })
		: t("browser.sitePermissions.prompt", {
				site,
				kinds: prompt.kinds.map((kind) => sitePermissionKindLabel(t, kind)).join(", "),
			});
	const Icon = external ? ExternalLink : ShieldQuestion;
	return (
		<div
			className="flex shrink-0 items-center gap-2 border-y border-border bg-background px-2.5 py-2 text-xs text-foreground"
			role="alert"
			data-testid="browser-site-permission-bar"
		>
			<Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
			<p className="m-0 min-w-0 flex-1 break-words leading-snug">{message}</p>
			<Button type="button" size="sm" variant="outline" className="shrink-0" onClick={() => respond(false)}>
				{external ? t("browser.sitePermissions.cancel") : t("browser.sitePermissions.block")}
			</Button>
			<Button type="button" size="sm" className="shrink-0" onClick={() => respond(true)}>
				{external ? t("browser.sitePermissions.open") : t("browser.sitePermissions.allow")}
			</Button>
		</div>
	);
}
