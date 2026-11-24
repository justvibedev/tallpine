import type { BrowserEntry, BrowserSessionEntry } from "./types";
import type { BrowserNetworkCapture, BrowserNetworkRequest, InternalBrowserNetworkRequest } from "./network-types";
import type { AgentBrowserDebugger } from "../agent-browser-cdp-bridge";
import { MAX_NETWORK_REQUESTS } from "./constants";
import { activeEntry } from "./entry-state";
import { sanitizeBrowserURL } from "./url";
import { browserError, finiteNumber, objectValue } from "./args";

/**
 * One tab whose requests can be recorded: an in-app browser entry or a Chrome
 * mode tab. Capture state lives on the tab; the CDP traffic comes from the
 * AgentBrowserDebugger of either backend.
 */
export type NetworkCaptureTab = {
  tabId: string;
  networkCapture?: BrowserNetworkCapture;
};

/** Attaches the debugger (shared by capture, agent-browser and DevTools) and enables the page domains. */
export async function ensureAgentDebugger(debug: AgentBrowserDebugger | undefined): Promise<AgentBrowserDebugger> {
  if (!debug)
    throw browserError(
      "BROWSER_TARGET_UNAVAILABLE",
      "Browser debugger is unavailable",
    );
  if (!debug.isAttached()) {
    try {
      debug.attach("1.3");
    } catch (error) {
      throw browserError(
        "BROWSER_TARGET_UNAVAILABLE",
        error instanceof Error
          ? error.message
          : "Unable to attach to browser target",
      );
    }
  }
  await debug.sendCommand("Runtime.enable");
  await debug.sendCommand("DOM.enable");
  return debug;
}

export async function ensureDebugger(entry: BrowserEntry): Promise<void> {
  await entry.ready;
  await ensureAgentDebugger(entry.view.webContents.debugger);
}

export function networkEntryFor(session: BrowserSessionEntry): BrowserEntry {
  if (session.networkTabId) {
    const captured = session.tabs.get(session.networkTabId);
    if (captured) return captured;
    session.networkTabId = undefined;
  }
  return activeEntry(session);
}

export async function startNetworkCapture(
  session: BrowserSessionEntry,
  entry: BrowserEntry,
  durationSeconds: number,
): Promise<unknown> {
  return startTabNetworkCapture(networkEntryFor(session), entry, durationSeconds, async () => {
    await entry.ready;
    return entry.view.webContents.debugger;
  }, () => {
    session.networkTabId = entry.tabId;
  });
}

/**
 * Starts recording on tab, unless the session's capture (existing) is still
 * active. debuggerFor resolves the tab's debugger once the old capture is
 * stopped; onStarted records which tab the session now captures.
 */
export async function startTabNetworkCapture(
  existing: NetworkCaptureTab,
  tab: NetworkCaptureTab,
  durationSeconds: number,
  debuggerFor: () => Promise<AgentBrowserDebugger | undefined>,
  onStarted: () => void,
): Promise<Record<string, unknown>> {
  if (existing.networkCapture?.active) {
    return { ...networkCaptureStatus(existing), alreadyActive: true };
  }
  if (existing !== tab) disposeNetworkCapture(existing, "restarted");
  disposeNetworkCapture(tab, "restarted");
  const debug = await ensureAgentDebugger(await debuggerFor());
  await debug.sendCommand("Network.enable");
  const started = Date.now();
  const capture: BrowserNetworkCapture = {
    active: true,
    tabId: tab.tabId,
    startedAt: new Date(started).toISOString(),
    expiresAt: new Date(started + durationSeconds * 1_000).toISOString(),
    maxEntries: MAX_NETWORK_REQUESTS,
    nextSequence: 1,
    requests: [],
    byRequestId: new Map(),
  };
  capture.timer = setTimeout(() => {
    void stopNetworkCapture(tab, "expired");
  }, durationSeconds * 1_000);
  tab.networkCapture = capture;
  onStarted();
  return networkCaptureStatus(tab);
}

export function networkCaptureStatus(entry: NetworkCaptureTab): Record<string, unknown> {
  const capture = entry.networkCapture;
  if (!capture) {
    return {
      active: false,
      metadataOnly: true,
      tabId: entry.tabId,
      requestCount: 0,
      maxEntries: MAX_NETWORK_REQUESTS,
      untrustedExternalContent: true,
    };
  }
  return {
    active: capture.active,
    metadataOnly: true,
    tabId: capture.tabId,
    requestCount: capture.requests.length,
    maxEntries: capture.maxEntries,
    startedAt: capture.startedAt,
    expiresAt: capture.expiresAt,
    ...(capture.stoppedAt ? { stoppedAt: capture.stoppedAt } : {}),
    ...(capture.stopReason ? { stopReason: capture.stopReason } : {}),
    untrustedExternalContent: true,
  };
}

export function networkCaptureResult(entry: NetworkCaptureTab): Record<string, unknown> {
  return {
    ...networkCaptureStatus(entry),
    requests: (entry.networkCapture?.requests ?? []).map(publicNetworkRequest),
    untrustedExternalContent: true,
  };
}

export async function stopNetworkCapture(
  entry: NetworkCaptureTab,
  reason: string,
): Promise<Record<string, unknown>> {
  const capture = entry.networkCapture;
  if (!capture?.active) return networkCaptureResult(entry);
  if (capture.timer) {
    clearTimeout(capture.timer);
    capture.timer = undefined;
  }
  capture.active = false;
  capture.stoppedAt = new Date().toISOString();
  capture.stopReason = reason;
  // The debugger attachment is shared by capture, agent-browser, and DevTools.
  // Stop recording locally, but leave the shared Network domain enabled until
  // the attachment itself is released.
  return networkCaptureResult(entry);
}

export function clearNetworkCapture(entry: NetworkCaptureTab): Record<string, unknown> {
  const capture = entry.networkCapture;
  if (capture) {
    capture.requests = [];
    capture.byRequestId.clear();
  }
  return networkCaptureStatus(entry);
}

export function disposeNetworkCapture(entry: NetworkCaptureTab, reason: string): void {
  const capture = entry.networkCapture;
  if (!capture) return;
  if (capture.timer) clearTimeout(capture.timer);
  capture.timer = undefined;
  capture.active = false;
  capture.stoppedAt = new Date().toISOString();
  capture.stopReason = reason;
}

export function handleNetworkDebuggerEvent(
  entry: NetworkCaptureTab,
  method: string,
  params: Record<string, unknown>,
): void {
  const capture = entry.networkCapture;
  if (!capture?.active || !method.startsWith("Network.")) return;

  const requestID =
    typeof params.requestId === "string" ? params.requestId : "";
  if (!requestID) return;
  const timestamp = finiteNumber(params.timestamp);

  if (method === "Network.requestWillBeSent") {
    const request = objectValue(params.request);
    const url = typeof request.url === "string" ? request.url : "";
    const previous = capture.byRequestId.get(requestID);
    const redirect = objectValue(params.redirectResponse);
    if (previous && Object.keys(redirect).length > 0) {
      applyNetworkResponse(previous, redirect);
      finishNetworkRequest(previous, timestamp);
      previous.redirectedTo = sanitizeBrowserURL(url);
    }
    const wallTime = finiteNumber(params.wallTime);
    const item: InternalBrowserNetworkRequest = {
      id: `n${capture.nextSequence++}`,
      protocolRequestId: requestID,
      method: typeof request.method === "string" ? request.method : "GET",
      url: sanitizeBrowserURL(url),
      resourceType:
        typeof params.type === "string" ? params.type.toLowerCase() : undefined,
      startedAt: wallTime
        ? new Date(wallTime * 1_000).toISOString()
        : new Date().toISOString(),
      startedMonotonic: timestamp,
      requestHeaders: selectedNetworkHeaders(request.headers, "request"),
    };
    appendNetworkRequest(capture, item);
    capture.byRequestId.set(requestID, item);
    return;
  }

  const item = capture.byRequestId.get(requestID);
  if (!item) return;
  switch (method) {
    case "Network.responseReceived":
      applyNetworkResponse(item, objectValue(params.response));
      break;
    case "Network.loadingFinished":
      finishNetworkRequest(item, timestamp);
      break;
    case "Network.loadingFailed":
      item.failed = true;
      item.canceled = params.canceled === true;
      item.errorText =
        typeof params.errorText === "string"
          ? params.errorText
          : "Request failed";
      finishNetworkRequest(item, timestamp);
      break;
    case "Network.requestServedFromCache":
      item.fromCache = true;
      break;
  }
}

function applyNetworkResponse(
  item: InternalBrowserNetworkRequest,
  response: Record<string, unknown>,
): void {
  const status = finiteNumber(response.status);
  if (status !== undefined) item.status = status;
  if (typeof response.statusText === "string" && response.statusText)
    item.statusText = response.statusText;
  if (typeof response.mimeType === "string" && response.mimeType)
    item.mimeType = response.mimeType;
  item.fromCache =
    item.fromCache === true ||
    response.fromDiskCache === true ||
    response.fromPrefetchCache === true;
  item.fromServiceWorker = response.fromServiceWorker === true;
  item.responseHeaders = selectedNetworkHeaders(response.headers, "response");
}

function finishNetworkRequest(
  item: InternalBrowserNetworkRequest,
  timestamp: number | undefined,
): void {
  if (timestamp !== undefined && item.startedMonotonic !== undefined) {
    item.durationMs = Math.max(
      0,
      Math.round((timestamp - item.startedMonotonic) * 1_000),
    );
  }
}

function appendNetworkRequest(
  capture: BrowserNetworkCapture,
  item: InternalBrowserNetworkRequest,
): void {
  capture.requests.push(item);
  if (capture.requests.length <= capture.maxEntries) return;
  const removed = capture.requests.shift();
  if (
    removed &&
    capture.byRequestId.get(removed.protocolRequestId) === removed
  ) {
    capture.byRequestId.delete(removed.protocolRequestId);
  }
}

function publicNetworkRequest(
  item: InternalBrowserNetworkRequest,
): BrowserNetworkRequest {
  const {
    protocolRequestId: _protocolRequestId,
    startedMonotonic: _startedMonotonic,
    ...result
  } = item;
  return result;
}

const SAFE_REQUEST_HEADERS = new Set([
  "accept",
  "content-type",
  "origin",
  "referer",
  "sec-fetch-mode",
  "sec-fetch-site",
]);

const SAFE_RESPONSE_HEADERS = new Set([
  "access-control-allow-headers",
  "access-control-allow-methods",
  "access-control-allow-origin",
  "cache-control",
  "content-length",
  "content-type",
  "location",
  "vary",
]);

function selectedNetworkHeaders(
  value: unknown,
  kind: "request" | "response",
): Record<string, string> | undefined {
  const headers = objectValue(value);
  const allowed =
    kind === "request" ? SAFE_REQUEST_HEADERS : SAFE_RESPONSE_HEADERS;
  const selected: Record<string, string> = {};
  for (const [rawName, rawValue] of Object.entries(headers)) {
    const name = rawName.toLowerCase();
    if (!allowed.has(name)) continue;
    let headerValue =
      typeof rawValue === "string" ? rawValue : String(rawValue);
    if (name === "referer" || name === "location")
      headerValue = sanitizeBrowserURL(headerValue);
    selected[name] = headerValue.slice(0, 1_000);
  }
  return Object.keys(selected).length > 0 ? selected : undefined;
}
