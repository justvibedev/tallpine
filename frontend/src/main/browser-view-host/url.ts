import { ALLOWED_PROTOCOLS } from "./constants";
import { browserError } from "./args";

export function normalizeBrowserURL(input: string): URL {
  const raw = input.trim();
  if (raw === "") {
    throw new Error("URL is required");
  }
  const candidate = withDefaultScheme(raw);
  const url = new URL(candidate);
  if (!ALLOWED_PROTOCOLS.has(url.protocol)) {
    throw new Error(`Unsupported browser URL scheme: ${url.protocol}`);
  }
  return url;
}

export function isAllowedBrowserURL(
  input: string,
  rendererOrigin?: string,
): boolean {
  try {
    const url = normalizeBrowserURL(input);
    if (rendererOrigin && url.origin === rendererOrigin) return false;
    return true;
  } catch {
    return false;
  }
}

function withDefaultScheme(raw: string): string {
  if (isWindowsAbsolutePath(raw) || isPosixAbsolutePath(raw))
    return localPathToFileURL(raw);
  if (/^https?:\/\//i.test(raw)) return raw;
  if (isLocalhostLike(raw)) return `http://${raw}`;
  // A single token with no whitespace can be a destination: an explicit scheme
  // (file:, mailto:, ...) or a bare hostname we default to https. Anything else —
  // whitespace-containing text, or a lone word that is not a hostname — is a
  // search query, not a URL (Chrome-style omnibox behavior).
  if (!/\s/.test(raw)) {
    if (/^[a-zA-Z][a-zA-Z\d+.-]*:/.test(raw)) return raw;
    if (looksLikeHost(raw)) return `https://${raw}`;
  }
  return searchURL(raw);
}

// Treat input as a navigable host when the authority (the part before any
// path/query/fragment) is an IPv6 literal, carries an explicit :port, or has a
// dot (a domain). Bare words like "hi" fail this and become a search instead.
function looksLikeHost(raw: string): boolean {
  const host = raw.split(/[/?#]/, 1)[0];
  if (host === "") return false;
  if (host.startsWith("[") && host.includes("]")) return true;
  if (/:\d+$/.test(host)) return true;
  return host.includes(".");
}

function searchURL(query: string): string {
  return `https://www.google.com/search?q=${encodeURIComponent(query)}`;
}

function isWindowsAbsolutePath(raw: string): boolean {
  return /^[a-zA-Z]:[\\/]/.test(raw);
}

function isPosixAbsolutePath(raw: string): boolean {
  return raw.startsWith("/");
}

function localPathToFileURL(raw: string): string {
  if (isWindowsAbsolutePath(raw)) {
    const normalized = raw.replace(/\\/g, "/");
    return `file:///${encodePathSegments(normalized).replace(/^([A-Za-z])%3A(?=\/)/, "$1:")}`;
  }
  return `file://${encodePathSegments(raw)}`;
}

function encodePathSegments(pathname: string): string {
  return pathname.split("/").map(encodeURIComponent).join("/");
}

function isLocalhostLike(raw: string): boolean {
  return /^(localhost|127(?:\.\d{1,3}){3}|0\.0\.0\.0|\[::1\])(?::\d+)?(?:[/?#]|$)/i.test(
    raw,
  );
}

// Safe-to-retain URL form: keep the location useful while removing values that
// commonly carry credentials or one-time handoff material.
export function sanitizeBrowserURL(raw: string): string {
  if (!raw) return "";
  if (raw === "about:blank") return raw;
  try {
    const url = new URL(raw);
    if (!["http:", "https:", "file:"].includes(url.protocol)) {
      return `${url.protocol}[redacted]`;
    }
    url.username = "";
    url.password = "";
    url.hash = "";
    for (const name of [...url.searchParams.keys()]) {
      url.searchParams.set(name, "[redacted]");
    }
    return url.href;
  } catch {
    const withoutFragment = raw.split("#", 1)[0] ?? "";
    const withoutQuery = withoutFragment.split("?", 1)[0] ?? "";
    return withoutQuery
      .replace(/^([a-z][a-z\d+.-]*:\/\/)[^/@\s]+@/i, "$1")
      .slice(0, 2_000);
  }
}

export function sanitizeBrowserTitle(raw: string): string {
  const title = raw.trim();
  if (
    /^(?:[a-z][a-z\d+.-]*:\/\/|(?:about|mailto|data|javascript|blob):)/i.test(
      title,
    )
  ) {
    return sanitizeBrowserURL(title);
  }
  return sanitizeURLsInText(raw);
}

export function sanitizeURLsInText(raw: string): string {
  return raw.replace(/\b(?:https?|file):\/\/[^\s<>"']+/gi, (match) =>
    sanitizeBrowserURL(match),
  );
}

export function normalizeAgentBrowserURL(input: string): string {
  const raw = input.trim();
  if (!raw) throw browserError("URL_REQUIRED", "url is required");
  if (
    isWindowsAbsolutePath(raw) ||
    isPosixAbsolutePath(raw) ||
    /^file:/i.test(raw)
  ) {
    throw browserError(
      "BROWSER_URL_FORBIDDEN",
      "Agent browser commands cannot open local files",
    );
  }
  if (
    !/^https?:\/\//i.test(raw) &&
    !isLocalhostLike(raw) &&
    !looksLikeHost(raw)
  ) {
    throw browserError(
      "INVALID_URL",
      "tallpine browser open requires an explicit http(s) URL or hostname",
    );
  }
  const normalized = normalizeBrowserURL(raw);
  if (normalized.protocol !== "http:" && normalized.protocol !== "https:") {
    throw browserError(
      "BROWSER_URL_FORBIDDEN",
      "Agent browser commands support only http(s) URLs",
    );
  }
  return normalized.href;
}
