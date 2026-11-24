import type { ContextMenuParams, MenuItemConstructorOptions } from "electron";
import { DEFAULT_LOCALE } from "../../shared/ui-locale";
import { trayText, type TrayText } from "../tray-text";
import type { TabWiring } from "./create-tab-steps";
import { sitePermissionScope } from "./site-permissions";
import type { TabNavCommand } from "./types";
import { searchURL } from "./url";

export type PageMenuParams = Pick<
  ContextMenuParams,
  "x" | "y" | "linkURL" | "srcURL" | "mediaType" | "selectionText" | "isEditable" | "misspelledWord" | "dictionarySuggestions" | "editFlags"
>;

export type PageEditCommand = "undo" | "redo" | "cut" | "copy" | "paste" | "selectAll";

/** What each item of a page's right-click menu does, for one tab. */
export type PageMenuActions = {
  openInNewTab(url: string): void;
  openInSystemBrowser(url: string): void;
  copyText(text: string): void;
  copyImageAt(x: number, y: number): void;
  saveURL(url: string): void;
  replaceMisspelling(suggestion: string): void;
  addToDictionary(word: string): void;
  edit(command: PageEditCommand): void;
  canGoBack(): boolean;
  canGoForward(): boolean;
  nav(command: Extract<TabNavCommand, "back" | "forward" | "reload">): void;
  inspect(x: number, y: number): void;
};

const SEARCH_LABEL_MAX = 32;

function isWebURL(raw: string): boolean {
  try {
    const { protocol } = new URL(raw);
    return protocol === "http:" || protocol === "https:";
  } catch {
    return false;
  }
}

function shortSelection(text: string): string {
  return text.length > SEARCH_LABEL_MAX ? `${text.slice(0, SEARCH_LABEL_MAX - 1)}…` : text;
}

/**
 * The native menu for a right-click on a person's page, built from Electron's
 * `context-menu` params the way Chrome groups it: link, image, spelling,
 * editing or selection, page navigation, then Inspect.
 */
export function pageMenuTemplate(params: PageMenuParams, actions: PageMenuActions, text: TrayText): MenuItemConstructorOptions[] {
  const { t } = text;
  const sections: MenuItemConstructorOptions[][] = [];
  const { linkURL, srcURL, misspelledWord, editFlags } = params;
  const selection = params.selectionText.replace(/\s+/g, " ").trim();
  if (linkURL) {
    const link: MenuItemConstructorOptions[] = [];
    if (isWebURL(linkURL)) {
      link.push(
        { label: t("pageMenu.openLinkInNewTab"), click: () => actions.openInNewTab(linkURL) },
        { label: t("pageMenu.openLinkInSystemBrowser"), click: () => actions.openInSystemBrowser(linkURL) },
      );
    }
    link.push({ label: t("pageMenu.copyLinkAddress"), click: () => actions.copyText(linkURL) });
    sections.push(link);
  }
  if (params.mediaType === "image" && srcURL) {
    const image: MenuItemConstructorOptions[] = [];
    if (isWebURL(srcURL)) image.push({ label: t("pageMenu.openImageInNewTab"), click: () => actions.openInNewTab(srcURL) });
    // Saved through the panel's download manager, like any other download.
    if (isWebURL(srcURL) || srcURL.startsWith("data:")) image.push({ label: t("pageMenu.saveImage"), click: () => actions.saveURL(srcURL) });
    image.push(
      { label: t("pageMenu.copyImage"), click: () => actions.copyImageAt(params.x, params.y) },
      { label: t("pageMenu.copyImageAddress"), click: () => actions.copyText(srcURL) },
    );
    sections.push(image);
  }
  if (misspelledWord) {
    const suggestions = params.dictionarySuggestions;
    sections.push([
      ...(suggestions.length
        ? suggestions.map((suggestion): MenuItemConstructorOptions => ({ label: suggestion, click: () => actions.replaceMisspelling(suggestion) }))
        : [{ label: t("pageMenu.noSuggestions"), enabled: false }]),
      { label: t("pageMenu.addToDictionary"), click: () => actions.addToDictionary(misspelledWord) },
    ]);
  }
  const search: MenuItemConstructorOptions[] = selection
    ? [{ label: t("pageMenu.searchFor", { text: shortSelection(selection) }), click: () => actions.openInNewTab(searchURL(selection)) }]
    : [];
  if (params.isEditable) {
    const edit = (command: PageEditCommand, enabled: boolean): MenuItemConstructorOptions => ({
      label: t(`pageMenu.${command}`),
      enabled,
      click: () => actions.edit(command),
    });
    sections.push([edit("undo", editFlags.canUndo), edit("redo", editFlags.canRedo)]);
    sections.push([
      edit("cut", editFlags.canCut),
      edit("copy", editFlags.canCopy),
      edit("paste", editFlags.canPaste),
      edit("selectAll", editFlags.canSelectAll),
    ]);
    if (search.length) sections.push(search);
  } else if (selection) {
    sections.push([{ label: t("pageMenu.copy"), click: () => actions.edit("copy") }, ...search]);
  } else if (!linkURL && params.mediaType !== "image") {
    sections.push([
      { label: t("pageMenu.back"), enabled: actions.canGoBack(), click: () => actions.nav("back") },
      { label: t("pageMenu.forward"), enabled: actions.canGoForward(), click: () => actions.nav("forward") },
      { label: t("pageMenu.reload"), click: () => actions.nav("reload") },
    ]);
  }
  sections.push([{ label: t("pageMenu.inspect"), click: () => actions.inspect(params.x, params.y) }]);
  return sections.flatMap((section, index) => (index === 0 ? section : [{ type: "separator" as const }, ...section]));
}

/**
 * Right-click on a person's own tab opens the native page menu. Agent tabs
 * (throwaway, proxied or granted partitions) and tabs showing the agent's own
 * browser get no menu at all: the same rule that keeps them deny-all for site
 * permissions (site-permissions.ts), checked at each click because a tab
 * becomes an agent viewer after it is created.
 */
export function wireContextMenu(w: TabWiring): void {
  const { session, entry, view, isCurrentEntry } = w;
  const { fns, hostState, options } = w.ctx;
  const buildMenu = options.buildPageMenu;
  if (!buildMenu) return;
  const contents = view.webContents;
  const ignore = () => undefined;
  const isActive = () => isCurrentEntry() && session.activeTabId === entry.tabId && !session.profileSwitching;
  const actions: PageMenuActions = {
    openInNewTab: (url) => {
      if (isCurrentEntry()) void fns.openTab(session, url, false).catch(ignore);
    },
    openInSystemBrowser: (url) => void options.shell.openExternal(url).catch(ignore),
    copyText: (text) => void Promise.resolve(options.clipboard?.writeText?.(text)).catch(ignore),
    copyImageAt: (x, y) => contents.copyImageAt?.(x, y),
    saveURL: (url) => contents.downloadURL?.(url),
    replaceMisspelling: (suggestion) => contents.replaceMisspelling?.(suggestion),
    addToDictionary: (word) => void contents.session?.addWordToSpellCheckerDictionary?.(word),
    edit: (command) => contents[command]?.(),
    canGoBack: () => entry.driver().page().canGoBack,
    canGoForward: () => entry.driver().page().canGoForward,
    nav: (command) => {
      if (!isActive()) return;
      try {
        fns.invokeNav(session.viewId, command, command !== "reload", command === "reload");
      } catch {
        // A profile switch began after the menu opened.
      }
    },
    inspect: (x, y) => {
      if (!isActive()) return;
      hostState.lastFocusedViewId = session.viewId;
      void fns
        .withBrowserOperation(session, () => fns.devtoolsAction(session, "open"))
        .then(() => {
          if (isActive()) contents.inspectElement?.(x, y);
        })
        .catch(ignore);
    },
  };
  contents.on("context-menu", (_event, params) => {
    if (!isCurrentEntry() || session.profileReplacing) return;
    if (sitePermissionScope(entry, session) === undefined) return;
    const text = trayText(options.getLocale?.() ?? DEFAULT_LOCALE);
    buildMenu(pageMenuTemplate(params, actions, text)).popup();
  });
}
