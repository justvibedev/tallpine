import { randomUUID } from "node:crypto";
import type { BrowserAnnotationCancelPayload, BrowserAnnotationContext, BrowserAnnotationSession, BrowserAnnotationStatePayload } from "../../shared/browser-annotations";
import type { BrowserEntry, BrowserViewHostOptions } from "./types";
import { shellContents } from "./entry-state";

function isValidAnnotationContext(
  value: unknown,
): value is BrowserAnnotationContext {
  if (typeof value !== "object" || value === null) return false;
  const context = value as {
    url?: unknown;
    tag?: unknown;
    classes?: unknown;
    selector?: unknown;
    size?: unknown;
    computedStyle?: unknown;
  };
  if (typeof context.url !== "string") return false;
  if (typeof context.tag !== "string") return false;
  if (!Array.isArray(context.classes)) return false;
  if (typeof context.selector !== "string") return false;
  if (typeof context.size !== "object" || context.size === null) return false;
  const size = context.size as { width?: unknown; height?: unknown };
  if (typeof size.width !== "number" || typeof size.height !== "number")
    return false;
  if (
    typeof context.computedStyle !== "object" ||
    context.computedStyle === null
  )
    return false;
  return true;
}

export function isValidAnnotationSession(
  value: unknown,
): value is BrowserAnnotationSession {
  if (!value || typeof value !== "object") return false;
  const session = value as Partial<BrowserAnnotationSession>;
  if (
    session.version !== 1 ||
    !session.page ||
    typeof session.page.url !== "string"
  )
    return false;
  if (
    !Array.isArray(session.annotations) ||
    !Array.isArray(session.screenshots)
  )
    return false;
  return session.annotations.every((annotation) =>
    Boolean(
      annotation &&
      typeof annotation.id === "string" &&
      typeof annotation.number === "number" &&
      (annotation.kind === "comment" || annotation.kind === "adjustment") &&
      typeof annotation.body === "string" &&
      annotation.target &&
      isValidAnnotationContext(annotation.target.context) &&
      Array.isArray(annotation.adjustments),
    ),
  );
}

export function cancelAnnotation(
  options: BrowserViewHostOptions,
  entry: BrowserEntry,
  reason: BrowserAnnotationCancelPayload["reason"],
): void {
  if (!entry.annotationEnabled) return;
  entry.annotationEnabled = false;
  entry.view.webContents.send("browser:annotation:setMode", { enabled: false });
  shellContents(options).send("browser:annotation:canceled", {
    viewId: entry.state.viewId,
    reason,
  });
}

export function annotationPageKey(url: string): string {
  try {
    const parsed = new URL(url);
    parsed.hash = "";
    return parsed.href;
  } catch {
    return url.split("#")[0] ?? url;
  }
}

export function annotationSessionFor(
  entry: BrowserEntry,
): BrowserAnnotationSession | undefined {
  return entry.annotationSessions.get(
    annotationPageKey(entry.view.webContents.getURL()),
  )?.session;
}

export function storeAnnotationSession(
  entry: BrowserEntry,
  session: BrowserAnnotationSession,
): { pageKey: string; token: string } {
  const pageKey = annotationPageKey(session.page.url);
  const token = randomUUID();
  entry.annotationSessions.set(pageKey, { session, token });
  return { pageKey, token };
}

export function pushAnnotationState(
  options: BrowserViewHostOptions,
  entry: BrowserEntry,
  annotationSession = annotationSessionFor(entry),
): void {
  const payload: BrowserAnnotationStatePayload = {
    viewId: entry.state.viewId,
    count: annotationSession?.annotations.length ?? 0,
    screenshotCount: annotationSession?.screenshots.length ?? 0,
    hasDraft: Boolean(annotationSession?.draft),
  };
  shellContents(options).send("browser:annotation:state", payload);
}

export function isSameAnnotationPage(source: string, destination: string): boolean {
  try {
    const sourceURL = new URL(source);
    const destinationURL = new URL(destination);
    sourceURL.hash = "";
    destinationURL.hash = "";
    return sourceURL.href === destinationURL.href;
  } catch {
    return source === destination;
  }
}
