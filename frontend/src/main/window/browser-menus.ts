import { clipboard, Menu, type BaseWindow } from "electron";
import type { BrowserProfileMenu, BrowserProfileMenuItem } from "../browser-profile-ipc";
import type { BrowserViewHostOptions } from "../browser-view-host";
import { writeClipboardImage } from "./renderer-assets";

/** The native profile switcher menu under a browser panel's profile button. */
export function buildProfileMenu(items: BrowserProfileMenuItem[]): BrowserProfileMenu {
	const menu = Menu.buildFromTemplate(items as Electron.MenuItemConstructorOptions[]);
	return { popup: (options) => menu.popup(options as Electron.PopupOptions) };
}

/** A person's page right-click menu (browser-view-host/context-menu.ts), shown in the main window. */
export function pageMenuOptions(window: () => BaseWindow | null): Pick<BrowserViewHostOptions, "buildPageMenu" | "clipboard"> {
	return {
		buildPageMenu: (items) => {
			const menu = Menu.buildFromTemplate(items);
			return { popup: () => menu.popup({ window: window() ?? undefined }) };
		},
		clipboard: { writeImage: writeClipboardImage, writeText: (text) => clipboard.writeText(text) },
	};
}
