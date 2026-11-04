import type { IpcMainEvent, IpcMainInvokeEvent, NativeImage } from "electron";
import type { BrowserAnnotationSnapshot } from "../../shared/browser-annotations";
import { cancelAnnotation, isSameAnnotationPage } from "./annotation-helpers";
import { clampBoundsToWindow, scaleBoundsForZoom } from "./bounds";
import { loadEntryReady } from "./load-entry";
import { browserError, throwIfAborted } from "./args";
import { pushNavState, readNavState } from "./nav-events";
import { isAllowedBrowserURL, normalizeBrowserURL } from "./url";
import { activeEntry, emptyNavState, isTemporaryBrowserPartition } from "./entry-state";
import type { BrowserBoundsInput, BrowserEntry, BrowserNavState, BrowserNavigateInput, BrowserSessionEntry } from "./types";
import { ANNOTATION_SNAPSHOT_MAX_DIMENSION, ANNOTATION_SNAPSHOT_TIMEOUT_MS, OFFSCREEN_BOUNDS, REMOTE_PREVIEW_URL, SCREENSHOT_FALLBACK_TIMEOUT_MS } from "./constants";
import type { HostContext } from "./context";

export function createBoundsNav(ctx: HostContext) {
  const { entries, entryReadyTimeoutMs, fns, forgetBrowserShortcutTarget, forgetNativeFocus, hostState, options, pendingTemporaryPartitionClears, rendererOwnersByViewId, shellWebContents } = ctx;
  const { applyBrowserViewBounds, destroyDevTools, openTab, syncTabLifecycle, withBrowserOperation } = ctx.fns;

  function applySessionBounds(
    session: BrowserSessionEntry,
    entry: BrowserEntry,
  ): void {
    syncTabLifecycle(session);
    fns.syncLiveViewport(session, entry);
    if (!session.visible) {
      if (session.live) {
        // A never-shown hidden WebContentsView has no compositor surface:
        // screencast emits nothing and captureScreenshot can wait forever.
        // Show the native view before moving it so Chromium updates its bounds,
        // but keep it completely outside the window while mobile is watching.
        entry.view.setVisible?.(true);
        applyBrowserViewBounds(entry.view, OFFSCREEN_BOUNDS);
        const parked = entry.view.getBounds?.();
        if (parked && (parked.x !== OFFSCREEN_BOUNDS.x || parked.y !== OFFSCREEN_BOUNDS.y ||
          parked.width !== OFFSCREEN_BOUNDS.width || parked.height !== OFFSCREEN_BOUNDS.height)) {
          entry.view.setVisible?.(false);
          session.live.sink.error("BROWSER_CAPTURE_UNAVAILABLE", "Browser capture could not be parked outside the desktop window");
        }
      } else {
        applyBrowserViewBounds(entry.view, OFFSCREEN_BOUNDS, false);
      }
      return;
    }
    // Keep the initialized blank target available to automation, but let the
    // renderer show Tallpine's empty-page UI instead of Chromium's white about:blank.
    // A destroyed WebContents (page called window.close()) leaves the view's
    // `webContents` undefined until the "destroyed" handler drops the tab.
    const currentURL = entry.view.webContents?.getURL();
    if (!currentURL || currentURL === "about:blank") {
      applyBrowserViewBounds(entry.view, session.bounds, false);
      return;
    }
    applyBrowserViewBounds(
      entry.view,
      session.bounds,
      session.bounds.width > 0 && session.bounds.height > 0,
    );
  }

  const isRendererOwned = (
    event: IpcMainInvokeEvent | IpcMainEvent,
    viewId: string,
  ): boolean =>
    rendererOwnersByViewId.get(viewId)?.has(event.sender.id) ?? false;

  function assertProfileStable(session: BrowserSessionEntry): void {
    if (session.profileSwitching) {
      throw browserError(
        "BROWSER_PROFILE_SWITCHING",
        "Browser profile switching is in progress",
      );
    }
  }

  // openTab/closeTab also run inside work admitted before a switch started
  // (agent tab-new/tab-close, popups), so they refuse only while tabs are
  // actually being replaced. Entry points check assertProfileStable.
  function assertTabsNotReplacing(session: BrowserSessionEntry): void {
    if (session.profileReplacing) {
      throw browserError(
        "BROWSER_PROFILE_SWITCHING",
        "Browser profile switching is in progress",
      );
    }
  }

  const setBounds = (
    { viewId, revision, rect, visible }: BrowserBoundsInput,
    zoomFactor = 1,
  ): BrowserBoundsInput | undefined => {
    const session = entries.get(viewId);
    if (
      !session ||
      !Number.isSafeInteger(revision) ||
      revision <= session.layoutRevision
    )
      return;
    session.layoutRevision = revision;
    const effectiveZoomFactor =
      Number.isFinite(zoomFactor) && zoomFactor > 0 ? zoomFactor : 1;
    session.zoomFactor = effectiveZoomFactor;
    if (!visible) {
      session.bounds = OFFSCREEN_BOUNDS;
      session.visible = false;
      if (!session.profileSwitching && session.tabs.size > 0)
        applySessionBounds(session, activeEntry(session));
      // Hiding the native surface (blank tab, transient measure) must not drop
      // the browser shortcut target — the panel chrome is still the context.
      forgetNativeFocus(viewId);
      return { viewId, revision, rect: session.bounds, visible: false };
    }
    // The renderer measures the slot in page-zoomed CSS pixels, while
    // WebContentsView bounds are window coordinates. Convert before clamping so
    // Cmd+/Cmd- page zoom does not detach the native view from its React slot.
    session.rendererBounds = { ...rect };
    session.bounds = clampBoundsToWindow(
      scaleBoundsForZoom(rect, effectiveZoomFactor),
      options.mainWindow.getContentBounds(),
    );
    session.visible = true;
    if (session.bounds.width > 0 && session.bounds.height > 0) {
      session.lastVisibleSize = {
        width: session.bounds.width,
        height: session.bounds.height,
      };
    }
    // A profile replacement may temporarily have no active tab. Keep accepting
    // renderer geometry during that interval; rebuilt tabs receive the latest
    // bounds instead of a stale pre-switch viewport.
    if (!session.profileSwitching && session.tabs.size > 0)
      applySessionBounds(session, activeEntry(session));
    // The shell toolbar can receive focus immediately after the Browser panel
    // becomes visible. Remember that active panel too, so the DevTools shortcut
    // still targets the browser even when the native page itself is not focused.
    hostState.lastFocusedViewId = viewId;
    return { viewId, revision, rect: session.bounds, visible: true };
  };

  const navigate = async ({
    viewId,
    url,
  }: BrowserNavigateInput): Promise<BrowserNavState> => {
    const session = entries.get(viewId);
    if (!session)
      throw browserError(
        "BROWSER_TARGET_UNAVAILABLE",
        "Browser target is unavailable",
      );
    assertProfileStable(session);
    return withBrowserOperation(session, async () => {
      if (!session.tabs.size) {
        const entry = await openTab(session, url, true);
        return pushNavState(options, entry);
      }
      return navigateEntry(activeEntry(session), url);
    });
  };

  const navigateEntry = async (
    entry: BrowserEntry,
    url: string,
    loadOptions?: Electron.LoadURLOptions,
  ): Promise<BrowserNavState> => {
    await entry.ready;
    const normalized = normalizeBrowserURL(url);
    if (!isAllowedBrowserURL(normalized.href, options.rendererOrigin)) {
      throw new Error("Unsupported browser URL");
    }
    return entry.driver().navigate(normalized.href, loadOptions);
  };

  // Loads a checked address into the tab's own view (navigateEntry).
  const loadEntry = async (
    entry: BrowserEntry,
    href: string,
    loadOptions?: Electron.LoadURLOptions,
  ): Promise<BrowserNavState> => {
    if (!isSameAnnotationPage(entry.view.webContents.getURL(), href)) {
      cancelAnnotation(options, entry, "navigation");
    }
    const owner = entries.get(entry.state.viewId);
    if (owner) owner.livePreviewPending = false;
    const pendingNavigation = { url: href };
    entry.pendingNavigation = pendingNavigation;
    try {
      if (loadOptions) await entry.view.webContents.loadURL(href, loadOptions);
      else await entry.view.webContents.loadURL(href);
    } catch (err) {
      if ((err as { errorCode?: number })?.errorCode === -3)
        return pushNavState(options, entry);
      entry.view.setVisible?.(false);
      entry.state = {
        ...readNavState(entry),
        error: String((err as Error)?.message || "Unable to load page"),
      };
      shellWebContents.send("browser:navState", entry.state);
      return entry.state;
    } finally {
      if (entry.pendingNavigation === pendingNavigation)
        entry.pendingNavigation = undefined;
    }
    const session = entries.get(entry.state.viewId);
    if (session?.activeTabId === entry.tabId)
      applySessionBounds(session, entry);
    return pushNavState(options, entry);
  };

  // clear resets the view to a blank page (`tallpine preview clear`). about:blank is
  // loaded directly, bypassing the URL allowlist — it carries no content and
  // readNavState normalizes it back to an empty url so the panel shows its
  // empty state.
  const clear = async (viewId: string): Promise<BrowserNavState> => {
    const session = entries.get(viewId);
    if (!session)
      throw browserError(
        "BROWSER_TARGET_UNAVAILABLE",
        "Browser target is unavailable",
      );
    assertProfileStable(session);
    session.livePreviewPending = false;
    return withBrowserOperation(session, async () => {
      if (!session.tabs.size) return emptyNavState(viewId);
      const active = activeEntry(session);
      // A cleared preview never blanks the agent's browser. With an agent tab
      // in front, the preview is in the person's tab behind it (where the
      // renderer's showPreview put it), which is blanked there.
      const entry =
        active.agentTab === undefined
          ? active
          : [...session.tabs.values()].find(
              (tab) => tab.agentTab === undefined && REMOTE_PREVIEW_URL.test(tab.view.webContents.getURL()),
            );
      if (!entry) return pushNavState(options, active);
      cancelAnnotation(options, entry, "navigation");
      if (entry === active) {
        if (session.devtools) destroyDevTools(session);
        session.visible = false;
        session.bounds = OFFSCREEN_BOUNDS;
        applySessionBounds(session, entry);
        forgetNativeFocus(viewId);
        forgetBrowserShortcutTarget(viewId);
      }
      entry.ready = loadEntryReady(entry.view, entryReadyTimeoutMs, () => {
        entry.ready = Promise.resolve();
      });
      await entry.ready;
      entry.view.webContents.clearHistory();
      return pushNavState(options, active);
    });
  };

  // Best-effort full-viewport page image shared by submit-time snapshots and
  // annotation screenshot copies. Bounded by ANNOTATION_SNAPSHOT_TIMEOUT_MS so
  // a slow/hung capturePage() can never block its caller — on timeout, error,
  // or an empty frame this resolves undefined.
  const capturePageImage = async (
    entry: BrowserEntry,
  ): Promise<NativeImage | undefined> => {
    try {
      const timedOut = Symbol("annotation-snapshot-timeout");
      const image = await Promise.race([
        entry.view.webContents.capturePage(),
        new Promise<typeof timedOut>((resolve) => {
          setTimeout(() => resolve(timedOut), ANNOTATION_SNAPSHOT_TIMEOUT_MS);
        }),
      ]);
      if (image === timedOut || image.isEmpty()) return undefined;
      return image;
    } catch {
      return undefined;
    }
  };

  // Best-effort full-viewport capture for a browser-annotation submit. On
  // timeout, error, or an empty frame this resolves undefined and the caller
  // proceeds with a text-only message.
  const captureAnnotationSnapshot = async (
    entry: BrowserEntry,
  ): Promise<BrowserAnnotationSnapshot | undefined> => {
    const image = await capturePageImage(entry);
    if (!image) return undefined;
    const { width, height } = image.getSize();
    const longestEdge = Math.max(width, height);
    const resized =
      longestEdge > ANNOTATION_SNAPSHOT_MAX_DIMENSION
        ? image.resize(
            width >= height
              ? { width: ANNOTATION_SNAPSHOT_MAX_DIMENSION }
              : { height: ANNOTATION_SNAPSHOT_MAX_DIMENSION },
          )
        : image;
    return { mimeType: "image/png", data: resized.toPNG().toString("base64") };
  };

  const captureScreenshotFallback = async (
    entry: BrowserEntry,
    signal?: AbortSignal,
  ): Promise<NativeImage> => {
    throwIfAborted(signal);
    let timer: ReturnType<typeof setTimeout> | undefined;
    let onAbort: (() => void) | undefined;
    try {
      return await new Promise<NativeImage>((resolve, reject) => {
        timer = setTimeout(
          () =>
            reject(
              browserError(
                "SCREENSHOT_UNAVAILABLE",
                "The browser page could not be captured in time",
              ),
            ),
          SCREENSHOT_FALLBACK_TIMEOUT_MS,
        );
        onAbort = () =>
          reject(
            browserError("BROWSER_COMMAND_CANCELED", "Browser command was canceled"),
          );
        signal?.addEventListener("abort", onAbort, { once: true });
        void entry.view.webContents.capturePage().then(resolve, reject);
      });
    } finally {
      if (timer !== undefined) clearTimeout(timer);
      if (onAbort) signal?.removeEventListener("abort", onAbort);
    }
  };

  const clearTemporaryPartition = (partition: string): Promise<void> => {
    const clearBrowserProfileData = options.clearBrowserProfileData;
    if (!isTemporaryBrowserPartition(partition) || !clearBrowserProfileData)
      return Promise.resolve();
    const cleanup = Promise.resolve()
      .then(() => clearBrowserProfileData(partition))
      .catch((error) => {
        console.warn("temporary browser profile cleanup failed:", error);
      });
    pendingTemporaryPartitionClears.add(cleanup);
    void cleanup.finally(() => pendingTemporaryPartitionClears.delete(cleanup));
    return cleanup;
  };

  return {
    applySessionBounds,
    assertProfileStable,
    assertTabsNotReplacing,
    captureAnnotationSnapshot,
    capturePageImage,
    captureScreenshotFallback,
    clear,
    clearTemporaryPartition,
    isRendererOwned,
    loadEntry,
    navigate,
    navigateEntry,
    setBounds,
  };
}
