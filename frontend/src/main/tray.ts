import { existsSync } from "node:fs";
import path from "node:path";
import { app, Menu, nativeImage, Tray, type MenuItemConstructorOptions } from "electron";
import { PRODUCT_NAME } from "../shared/brand";
import type { TrayMessageKey } from "./tray-catalog.generated";
import { trayText } from "./tray-text";
import type { AppLocale } from "../shared/ui-locale";
import type { TrayAttentionState, TrayAttentionZone, TrayOpenSessionTarget, TraySessionEntry } from "../shared/tray";

const MAX_MENU_SESSIONS = 8;

const zoneRank: Record<TrayAttentionZone, number> = { merge: 0, action: 1 };

const zoneLabelKey: Record<TrayAttentionZone, TrayMessageKey> = { merge: "zone.merge", action: "zone.action" };

function trayIconPath(): string | undefined {
	const candidate = app.isPackaged
		? path.join(process.resourcesPath, "trayIconTemplate.png")
		: path.join(__dirname, "../../assets/trayIconTemplate.png");
	return existsSync(candidate) ? candidate : undefined;
}

export type TrayController = {
	setState(state: TrayAttentionState): void;
	setLocale(locale: AppLocale): void;
	clear(): void;
	dispose(): void;
};

export type TrayControllerOptions = {
	focusWindow: () => void;
	openSession: (target: TrayOpenSessionTarget) => void;
	quitCompletely: () => void;
	/** Stops every running agent, after asking. Absent hides the item. */
	stopAllAgents?: () => void;
	locale: AppLocale;
};

export function createTrayController(options: TrayControllerOptions): TrayController | null {
	const iconPath = trayIconPath();
	if (!iconPath) return null;
	const icon = nativeImage.createFromPath(iconPath);
	if (icon.isEmpty()) return null;
	icon.setTemplateImage(true);

	const tray = new Tray(icon);
	let sessions: TraySessionEntry[] = [];
	let text = trayText(options.locale);
	const t: typeof text.t = (key, vars) => text.t(key, vars);
	const tPlural: typeof text.plural = (base, count) => text.plural(base, count);

	const sessionItem = (entry: TraySessionEntry): MenuItemConstructorOptions => {
		const title = entry.title || t("tray.untitledSession");
		return {
			label: entry.projectName ? `${title}  ·  ${entry.projectName}` : title,
			click: () => options.openSession({ projectId: entry.projectId, sessionId: entry.sessionId }),
		};
	};

	const render = () => {
		const count = sessions.length;
		tray.setTitle(count > 0 ? String(count) : "");
		tray.setToolTip(count > 0 ? tPlural("tray.attentionTooltip", count) : PRODUCT_NAME);

		const items: MenuItemConstructorOptions[] = [];
		if (count === 0) {
			items.push({ label: t("tray.empty"), enabled: false });
		} else {
			const ordered = [...sessions].sort(
				(a, b) => zoneRank[a.zone] - zoneRank[b.zone] || a.title.localeCompare(b.title),
			);
			const visible = ordered.slice(0, MAX_MENU_SESSIONS);
			const overflow = ordered.slice(MAX_MENU_SESSIONS);

			let lastZone: TrayAttentionZone | null = null;
			for (const entry of visible) {
				if (entry.zone !== lastZone) {
					if (lastZone !== null) items.push({ type: "separator" });
					items.push({ label: t(zoneLabelKey[entry.zone]), enabled: false });
					lastZone = entry.zone;
				}
				items.push(sessionItem(entry));
			}
			if (overflow.length > 0) {
				items.push({ type: "separator" });
				items.push({ label: `${t("tray.more")} (${overflow.length})`, submenu: overflow.map(sessionItem) });
			}
		}
		items.push({ type: "separator" });
		items.push({ label: t("tray.show"), click: () => options.focusWindow() });
		const { stopAllAgents } = options;
		if (stopAllAgents) items.push({ label: t("tray.stopAgents"), click: () => stopAllAgents() });
		items.push({ label: t("tray.quit"), click: () => options.quitCompletely() });
		tray.setContextMenu(Menu.buildFromTemplate(items));
	};

	render();

	return {
		setState(state) {
			sessions = state.sessions ?? [];
			render();
		},
		setLocale(locale) {
			text = trayText(locale);
			render();
		},
		clear() {
			sessions = [];
			render();
		},
		dispose() {
			tray.destroy();
		},
	};
}
