import { type Session, type WebContents, nativeImage } from "electron";
import type { BrowserEntry, BrowserWebContents } from "./types";
import { FAVICON_REQUEST_TIMEOUT_MS, FAVICON_SIZE, MAX_FAVICON_BYTES } from "./constants";

// A same-tab navigation to a different site (search result, link, address bar)
// keeps the previous site's favicon displayed until the new one has been
// fetched and decoded — a real network round trip — which reads as a laggy
// icon swap. Clear it as soon as the new origin is known (favicons are
// near-always origin-scoped, so a same-origin navigation likely keeps the
// same one and is left alone) so the rail falls back to the generic icon
// immediately instead of showing the *wrong* one while it waits. Called from
// wireNavEvents' own did-navigate handler rather than registering a second
// listener for the same event.
export function clearStaleFavicon(entry: BrowserEntry, url: string): void {
  // An agent tab's icon is its agent page's, which the viewer's own loads
  // (a reload, a move to a new origin) do not change.
  if (entry.agentTab !== undefined) return;
  const origin = originOf(url);
  if (origin && origin === entry.faviconOrigin) return;
  entry.favicon = undefined;
  entry.faviconSourceUrl = undefined;
  entry.faviconPendingUrl = undefined;
  entry.faviconOrigin = undefined;
}

export function wireFaviconEvents(
  contents: BrowserWebContents,
  entry: BrowserEntry,
  syncTabs: () => void,
): void {
  contents.on("page-favicon-updated", (_event, favicons) => {
    // An agent tab shows its agent page's icon, from its viewer's reports;
    // the viewer page's own icon is never fetched.
    if (entry.agentTab !== undefined) return;
    const url = favicons[0];
    // Not yet applied (faviconSourceUrl) and not already in flight
    // (faviconPendingUrl) — a page re-announcing the same favicon while a
    // prior fetch for it is still pending must not start a duplicate.
    if (
      !url ||
      url === entry.faviconSourceUrl ||
      url === entry.faviconPendingUrl
    )
      return;
    entry.faviconPendingUrl = url;
    void fetchFavicon(entry, url).then((dataUrl) => {
      if (entry.faviconPendingUrl === url) entry.faviconPendingUrl = undefined;
      // Leave faviconSourceUrl unset on failure (rather than marking the
      // URL "seen") so a later page-favicon-updated event for the same
      // URL — browsers re-fire these on soft/in-page navigations — gets a
      // fresh attempt instead of being permanently stuck on the fallback
      // icon from one transient fetch failure.
      if (entry.faviconSourceUrl !== url && dataUrl) {
        entry.faviconSourceUrl = url;
        entry.favicon = dataUrl;
        entry.faviconOrigin = originOf(url);
        syncTabs();
      }
    });
  });
}

export function originOf(url: string): string | undefined {
  try {
    return new URL(url).origin;
  } catch {
    return undefined;
  }
}

// Fetched through the tab's own isolated partition (not the shell session), so
// it carries whatever cookies/proxy config that site's tab already has, and
// resized/re-encoded like other browser-view thumbnail capture in this file.
async function fetchFavicon(
  entry: BrowserEntry,
  url: string,
): Promise<string | undefined> {
  const tabSession = (entry.view.webContents as unknown as WebContents).session;
  return fetchFaviconFromSession(tabSession, url);
}

export async function fetchFaviconFromSession(
  tabSession: Session,
  url: string,
): Promise<string | undefined> {
  const controller = new AbortController();
  const timeout = setTimeout(
    () => controller.abort(),
    FAVICON_REQUEST_TIMEOUT_MS,
  );
  try {
    // Some sites inline a tiny favicon as a data: URI rather than serving a
    // file — decode it directly instead of rejecting it as an unsupported
    // scheme, still normalized/capped through the same resize path.
    if (url.startsWith("data:")) {
      const image = nativeImage.createFromDataURL(url);
      if (image.isEmpty()) return undefined;
      return image
        .resize({ width: FAVICON_SIZE, height: FAVICON_SIZE, quality: "good" })
        .toDataURL();
    }
    const parsed = new URL(url);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:")
      return undefined;
    const response = await tabSession.fetch(url, { signal: controller.signal });
    if (!response.ok) return undefined;
    const buffer = await readBoundedResponseBody(response, MAX_FAVICON_BYTES);
    if (!buffer || buffer.byteLength === 0) return undefined;
    try {
      const image = nativeImage.createFromBuffer(buffer);
      if (!image.isEmpty()) {
        return image
          .resize({
            width: FAVICON_SIZE,
            height: FAVICON_SIZE,
            quality: "good",
          })
          .toDataURL();
      }
    } catch {
      // Fall through to the validated ICO path below. Test environments and
      // some Electron platforms throw rather than returning an empty image.
    }
    // nativeImage does not decode ICO buffers on every platform (notably
    // macOS), while Chromium's <img> decoder does. Preserve a bounded,
    // structurally valid ICO as local image data rather than falling back to
    // a direct renderer request that would escape the selected profile.
    if (isIcoBuffer(buffer))
      return `data:image/x-icon;base64,${buffer.toString("base64")}`;
    return undefined;
  } catch {
    return undefined;
  } finally {
    clearTimeout(timeout);
  }
}

function isIcoBuffer(buffer: Buffer): boolean {
  return (
    buffer.byteLength >= 6 &&
    buffer.readUInt16LE(0) === 0 &&
    buffer.readUInt16LE(2) === 1 &&
    buffer.readUInt16LE(4) > 0
  );
}

async function readBoundedResponseBody(
  response: Response,
  maxBytes: number,
): Promise<Buffer | undefined> {
  const declaredLength = Number(response.headers.get("content-length"));
  if (Number.isFinite(declaredLength) && declaredLength > maxBytes) {
    await response.body?.cancel().catch(() => undefined);
    return undefined;
  }
  const reader = response.body?.getReader();
  if (!reader) return undefined;
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      if (!value) continue;
      total += value.byteLength;
      if (total > maxBytes) {
        await reader.cancel().catch(() => undefined);
        return undefined;
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  return Buffer.concat(
    chunks.map((chunk) => Buffer.from(chunk)),
    total,
  );
}
