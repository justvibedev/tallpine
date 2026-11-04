import type { IpcMainEvent, IpcMainInvokeEvent } from "electron";
import type { BrowserAnnotationCancelPayload, BrowserAnnotationActionInput, BrowserAnnotationCompleteInput, BrowserAnnotationDiscardInput, BrowserAnnotationModeInput, BrowserAnnotationPageCancelPayload, BrowserAnnotationPageSubmitPayload, BrowserAnnotationSession, BrowserAnnotationSubmitPayload } from "../../shared/browser-annotations";
import { annotationPageKey, annotationSessionFor, cancelAnnotation, isValidAnnotationSession, pushAnnotationState, storeAnnotationSession } from "./annotation-helpers";
import { pushNavState } from "./nav-events";
import { activeEntry, emptyNavState } from "./entry-state";
import type { BrowserEntry, BrowserNavState, TabNavCommand } from "./types";
import type { HostContext } from "./context";

export function createAnnotation(ctx: HostContext) {
  const { entries, options, shellWebContents, tabsByWebContentsId } = ctx;
  const { applySessionBounds, assertProfileStable, captureAnnotationSnapshot, capturePageImage, isRendererOwned, withBrowserOperation } = ctx.fns;

  const invokeNav = (
    viewId: string,
    command: TabNavCommand,
    cancelForNavigation = false,
    reapplyBounds = false,
  ): BrowserNavState => {
    const session = entries.get(viewId);
    if (!session || !session.tabs.size) return emptyNavState(viewId);
    assertProfileStable(session);
    const entry = activeEntry(session);
    if (cancelForNavigation) {
      cancelAnnotation(options, entry, "navigation");
    }
    if (cancelForNavigation || reapplyBounds) {
      applySessionBounds(session, entry);
    }
    entry.driver().nav(command);
    return pushNavState(options, entry);
  };

  const setAnnotationMode = (
    event: IpcMainInvokeEvent,
    input: BrowserAnnotationModeInput,
  ): void => {
    if (!isRendererOwned(event, input.viewId)) return;
    const browserSession = entries.get(input.viewId);
    if (!browserSession || !browserSession.tabs.size) return;
    assertProfileStable(browserSession);
    const entry = activeEntry(browserSession);
    // An agent tab shows the agent's page as its viewer's picture: a pick
    // would land on the viewer's canvas and report the viewer's address.
    if (input.enabled && entry.agentTab !== undefined) return;
    if (!input.enabled) {
      // Drop the open composer when leaving annotation mode. Keep saved
      // batch annotations/markers so they reappear on the next entry.
      const stored = annotationSessionFor(entry);
      if (stored?.draft) {
        delete stored.draft;
        pushAnnotationState(options, entry, stored);
      }
    }
    entry.annotationEnabled = input.enabled;
    if (input.theme) entry.annotationTheme = input.theme;
    const annotationSession = annotationSessionFor(entry);
    entry.view.webContents.send("browser:annotation:setMode", {
      enabled: input.enabled,
      ...(annotationSession ? { session: annotationSession } : {}),
      ...(entry.annotationTheme ? { theme: entry.annotationTheme } : {}),
    });
    if (input.enabled) entry.view.webContents.focus();
  };

  const updateAnnotationState = (
    event: IpcMainEvent,
    annotationSession: BrowserAnnotationSession | undefined,
  ): void => {
    const entry = tabsByWebContentsId.get(event.sender.id);
    if (!entry || !isValidAnnotationSession(annotationSession)) return;
    storeAnnotationSession(entry, annotationSession);
    pushAnnotationState(options, entry, annotationSession);
  };

  const forwardAnnotationSubmit = async (
    event: IpcMainInvokeEvent,
    payload: BrowserAnnotationPageSubmitPayload | undefined,
  ): Promise<void> => {
    const entry = tabsByWebContentsId.get(event.sender.id);
    const viewId = entry?.state.viewId;
    // Only the shell turns annotation mode on; a page that is not in it cannot
    // submit feedback to the agent.
    if (
      !viewId ||
      !entry ||
      !entry.annotationEnabled ||
      !payload ||
      !isValidAnnotationSession(payload.session)
    )
      return;
    const browserSession = entries.get(viewId);
    if (
      !browserSession ||
      browserSession.profileSwitching ||
      browserSession.tabs.get(entry.tabId) !== entry
    )
      return;
    const { pageKey, token: sessionToken } = storeAnnotationSession(
      entry,
      payload.session,
    );
    await withBrowserOperation(browserSession, async () => {
      entry.annotationEnabled = false;
      // Captured now, before returning: the preload only tears down the
      // highlight overlay after this handler resolves, so the frame we grab
      // here still has the selection ring(s) on it and not the prompt box
      // (the preload hides that synchronously before invoking).
      const snapshot = await captureAnnotationSnapshot(entry);
      if (
        entries.get(viewId) !== browserSession ||
        browserSession.tabs.get(entry.tabId) !== entry ||
        browserSession.profileSwitching
      ) {
        return;
      }
      const forwarded: BrowserAnnotationSubmitPayload = {
        viewId,
        tabId: entry.tabId,
        pageKey,
        sessionToken,
        session: payload.session,
        ...(snapshot ? { snapshot } : {}),
      };
      shellWebContents.send("browser:annotation:submitted", forwarded);
    });
  };

  const forwardAnnotationCancel = (
    event: IpcMainEvent,
    payload: BrowserAnnotationPageCancelPayload | undefined,
  ): void => {
    const entry = tabsByWebContentsId.get(event.sender.id);
    const viewId = entry?.state.viewId;
    if (!viewId || !entry) return;
    entry.annotationEnabled = false;
    const forwarded: BrowserAnnotationCancelPayload = {
      viewId,
      reason: payload?.reason ?? "cancel",
    };
    shellWebContents.send("browser:annotation:canceled", forwarded);
  };

  // The annotation toolbar's screenshot button copies the capture to the
  // user's system clipboard instead of queueing an attachment for the next
  // agent batch. Resolves true only when the image actually reached the
  // clipboard, so the page can surface a failure instead of a false "copied".
  const captureAnnotation = async (
    event: IpcMainInvokeEvent,
  ): Promise<boolean> => {
    const entry = tabsByWebContentsId.get(event.sender.id);
    if (!entry || !entry.annotationEnabled || !options.clipboard) return false;
    const image = await capturePageImage(entry);
    if (!image) return false;
    await options.clipboard.writeImage(image);
    return true;
  };

  const discardAnnotationSession = (entry: BrowserEntry): void => {
    entry.annotationSessions.delete(
      annotationPageKey(entry.view.webContents.getURL()),
    );
    entry.view.webContents.send("browser:annotation:setMode", {
      enabled: entry.annotationEnabled,
      ...(entry.annotationTheme ? { theme: entry.annotationTheme } : {}),
    });
    pushAnnotationState(options, entry);
  };

  const discardAnnotationFromPage = (event: IpcMainEvent): void => {
    const entry = tabsByWebContentsId.get(event.sender.id);
    if (entry) discardAnnotationSession(entry);
  };

  const completeAnnotation = (
    event: IpcMainInvokeEvent,
    input: BrowserAnnotationCompleteInput,
  ): void => {
    if (!isRendererOwned(event, input.viewId)) return;
    const browserSession = entries.get(input.viewId);
    if (!browserSession) return;
    const entry = browserSession.tabs.get(input.tabId);
    if (!entry) return;
    const stored = entry.annotationSessions.get(input.pageKey);
    if (!stored || stored.token !== input.sessionToken) return;
    entry.annotationEnabled = false;
    if (input.success) entry.annotationSessions.delete(input.pageKey);
    if (activeEntry(browserSession) !== entry) return;
    entry.view.webContents.send("browser:annotation:setMode", {
      enabled: false,
      ...(annotationSessionFor(entry)
        ? { session: annotationSessionFor(entry) }
        : {}),
      ...(entry.annotationTheme ? { theme: entry.annotationTheme } : {}),
    });
    pushAnnotationState(options, entry);
  };

  const discardAnnotationFromShell = (
    event: IpcMainInvokeEvent,
    input: BrowserAnnotationDiscardInput,
  ): void => {
    if (!isRendererOwned(event, input.viewId)) return;
    const browserSession = entries.get(input.viewId);
    if (browserSession?.tabs.size) discardAnnotationSession(activeEntry(browserSession));
  };

  const forwardAnnotationAction = (
    event: IpcMainInvokeEvent,
    input: BrowserAnnotationActionInput,
  ): void => {
    if (!isRendererOwned(event, input.viewId)) return;
    const browserSession = entries.get(input.viewId);
    if (!browserSession || !browserSession.tabs.size) return;
    const entry = activeEntry(browserSession);
    if (!entry.annotationEnabled) return;
    entry.view.webContents.send("browser:annotation:action", input.action);
  };

  return {
    captureAnnotation,
    completeAnnotation,
    discardAnnotationFromPage,
    discardAnnotationFromShell,
    forwardAnnotationAction,
    forwardAnnotationCancel,
    forwardAnnotationSubmit,
    invokeNav,
    setAnnotationMode,
    updateAnnotationState,
  };
}
