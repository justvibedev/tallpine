import type { WebContents } from "electron";
import type { BrowserEntry, BrowserNavState, BrowserPopupOptions, BrowserSessionEntry, BrowserTabsState, BrowserViewHostOptions, BrowserWebContents } from "./types";
import { shellContents } from "./entry-state";
import { isAllowedBrowserURL } from "./url";
import { annotationSessionFor, cancelAnnotation, isSameAnnotationPage, pushAnnotationState } from "./annotation-helpers";
import { tabResult } from "./agent-results";
import { clearStaleFavicon } from "./favicon";
import { handleNetworkDebuggerEvent } from "./network-capture";
import { passkeyExternalUrl, trackLastNonIdpUrl } from "../../shared/browser-passkey-url";

export function listTabs(
  session: BrowserSessionEntry,
  change?: BrowserTabsState["change"],
): BrowserTabsState & { untrustedExternalContent: true } {
  return {
    viewId: session.viewId,
    activeTabId: session.activeTabId,
    tabs: [...session.tabs.values()].map((entry) =>
      tabResult(entry, entry.tabId === session.activeTabId),
    ),
    untrustedExternalContent: true,
    ...(change ? { change } : {}),
  };
}

export function pushTabsState(
  options: BrowserViewHostOptions,
  session: BrowserSessionEntry,
  change?: BrowserTabsState["change"],
): BrowserTabsState {
  const state = listTabs(session, change);
  shellContents(options).send("browser:tabsState", state);
  return state;
}

export function hardenWebContents(
  contents: BrowserWebContents,
  options: BrowserViewHostOptions,
  entry: BrowserEntry,
  isCurrent: () => boolean,
  popupWebPreferences: Electron.WebPreferences,
  createPopup: (
    windowOptions: BrowserPopupOptions,
    details: Electron.HandlerDetails,
  ) => WebContents,
  // Takes a mailto:/tel: link the page follows (it asks the person before
  // opening it); true means the link was taken and must not load here.
  takeExternalUrl: (url: string) => boolean = () => false,
): void {
  contents.setWindowOpenHandler((details) => {
    const { url } = details;
    if (isCurrent() && takeExternalUrl(url)) return { action: "deny" };
    if (
      !isCurrent() ||
      (url !== "about:blank" && !isAllowedBrowserURL(url, options.rendererOrigin))
    ) {
      return { action: "deny" };
    }
    return {
      action: "allow",
      overrideBrowserWindowOptions: { webPreferences: popupWebPreferences },
      createWindow: (windowOptions) => createPopup(windowOptions, details),
    };
  });
  const blockUnsafeNavigation = (event: Electron.Event, url: string) => {
    if (isCurrent() && takeExternalUrl(url)) {
      event.preventDefault();
      return;
    }
    if (!isAllowedBrowserURL(url, options.rendererOrigin)) {
      event.preventDefault();
      if (!isCurrent()) return;
      entry.state = { ...entry.state, error: "Unsupported browser URL" };
      shellContents(options).send("browser:navState", entry.state);
      return;
    }
    if (!isSameAnnotationPage(contents.getURL(), url)) {
      cancelAnnotation(options, entry, "navigation");
    }
  };
  contents.on("will-navigate", blockUnsafeNavigation);
  contents.on("will-redirect", blockUnsafeNavigation);
}

export function wireNavEvents(
  contents: BrowserWebContents,
  options: BrowserViewHostOptions,
  entry: BrowserEntry,
  isActive: () => boolean,
  syncActiveBounds: () => void,
  syncTabs: () => void,
  recordHistory: (url: string, title: string, incrementVisit: boolean) => void,
  resetFind: () => void,
): void {
  const update = () => {
    syncTabs();
    if (isActive()) pushNavState(options, entry);
  };
  contents.on("did-navigate", (_event, url) => {
    resetFind();
    entry.documentGeneration += 1;
    entry.navigationGeneration += 1;
    // A new document drops the old one's pending WebAuthn request.
    entry.passkeyPending = false;
    // ...and every site permission prompt it was waiting on.
    dropSitePrompts(entry);
    entry.lastNonIdpUrl = trackLastNonIdpUrl(entry.lastNonIdpUrl, url);
    clearStaleFavicon(entry, url);
    if (isActive()) syncActiveBounds();
    recordHistory(url, contents.getTitle(), true);
    update();
  });
  contents.on("did-navigate-in-page", (_event, url, isMainFrame) => {
    // Subframe hash/history changes are not application-level navigation for
    // either action postconditions or top-level browser history.
    if (isMainFrame === false) return;
    entry.lastNonIdpUrl = trackLastNonIdpUrl(entry.lastNonIdpUrl, url);
    entry.navigationGeneration += 1;
    recordHistory(url, contents.getTitle(), true);
    update();
  });
  // Electron emits this when a beforeunload handler cancels a main-frame
  // navigation. Keep only a monotonic fact: action correlation snapshots the
  // value before dispatch, so an unrelated earlier guard cannot taint a later
  // command and we never change Chromium's default cancellation behavior.
  contents.on("will-prevent-unload", () => {
    entry.blockedNavigationGeneration += 1;
  });
  contents.on("page-title-updated", update);
  contents.on("did-start-loading", () => {
    update();
  });
  contents.on("did-stop-loading", () => {
    const annotationSession = annotationSessionFor(entry);
    contents.send("browser:annotation:setMode", {
      enabled: entry.annotationEnabled,
      ...(annotationSession ? { session: annotationSession } : {}),
      ...(entry.annotationTheme ? { theme: entry.annotationTheme } : {}),
    });
    if (entry.annotationEnabled) contents.focus();
    pushAnnotationState(options, entry, annotationSession);
    recordHistory(contents.getURL(), contents.getTitle(), false);
    update();
  });
  contents.on(
    "did-fail-load",
    (_event, errorCode, errorDescription, _validatedURL, isMainFrame) => {
      if (errorCode === -3) return;
      // A page can contain third-party images, frames, or scripts that fail
      // independently. Only a main-frame failure means the browser page itself
      // should be replaced with Tallpine's error state.
      if (isMainFrame === false) return;
      cancelAnnotation(options, entry, "navigation");
      if (isActive()) entry.view.setVisible?.(false);
      entry.state = {
        ...readNavState(entry),
        error: String(errorDescription || "Unable to load page"),
      };
      if (isActive())
        shellContents(options).send("browser:navState", entry.state);
    },
  );
}

export function pushNavState(
  options: BrowserViewHostOptions,
  entry: BrowserEntry,
): BrowserNavState {
  entry.state = readNavState(entry);
  shellContents(options).send("browser:navState", entry.state);
  return entry.state;
}

// Drives the agent browser page an agent tab shows, through its viewer.
export function runInRemoteViewer(entry: BrowserEntry, call: string): Promise<unknown> {
  return entry.view.webContents
    .executeJavaScript(`window.__rbHost && window.__rbHost.${call}`)
    .catch(() => undefined);
}

// Whether an agent tab's viewer page runs. One that failed to load (Chromium's
// error page, or the proxy's 404 while its host was away) keeps the viewer's
// address but has no script: nothing drives the agent's page through it, and
// no socket comes back by itself.
export function viewerRuns(entry: BrowserEntry): Promise<boolean> {
  return entry.view.webContents
    .executeJavaScript("typeof window.__rbHost === 'object'")
    .then((runs) => runs === true, () => false);
}

function passkeyNavFields(entry: BrowserEntry, url: string): Pick<BrowserNavState, "passkeyPending" | "passkeyExternalUrl"> {
  const external = passkeyExternalUrl(url, entry.lastNonIdpUrl);
  return { passkeyPending: true, ...(external ? { passkeyExternalUrl: external } : {}) };
}

/** Answers every prompt a tab still shows with "no": its page went away. */
export function dropSitePrompts(entry: BrowserEntry): void {
  const pending = entry.sitePrompts?.splice(0) ?? [];
  for (const prompt of pending) prompt.resolve(false);
}

function siteNavFields(entry: BrowserEntry): Pick<BrowserNavState, "sitePermissionPrompt" | "sitePermissions"> {
  const prompt = entry.sitePrompts?.[0]?.prompt;
  const remembered = entry.siteSettings?.() ?? [];
  return { ...(prompt ? { sitePermissionPrompt: prompt } : {}), ...(remembered.length ? { sitePermissions: remembered } : {}) };
}

export function readNavState(entry: BrowserEntry): BrowserNavState {
  const page = entry.driver().page();
  return {
    viewId: entry.state.viewId,
    ...page,
    // about:blank is the cleared/blank state — surface it as an empty url so
    // the panel renders its "enter a URL" empty state and the address bar is
    // blank rather than showing "about:blank".
    url: page.url === "about:blank" ? "" : page.url,
    ...(entry.passkeyPending ? passkeyNavFields(entry, page.url) : {}),
    ...siteNavFields(entry),
  };
}

export function wireAutomationEvents(
  contents: BrowserWebContents,
  entry: BrowserEntry,
): void {
  contents.debugger?.on("message", (_event, method, params) => {
    handleNetworkDebuggerEvent(
      entry,
      method,
      params as Record<string, unknown>,
    );
  });
}
