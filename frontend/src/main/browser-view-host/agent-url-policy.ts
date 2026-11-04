import { browserError } from "./args";
import { normalizeAgentBrowserURL } from "./url";

// Policy for URLs an agent drives a browser tab to (tallpine browser commands and the
// CDP bridge). Human-typed navigation does not go through this. Only http(s) is
// allowed, and IP-literal hosts in private or link-local ranges are refused so
// a page cannot steer the agent at internal services or cloud metadata.
// Loopback stays allowed: it is where `tallpine preview` and dev servers are served.
// Hostnames that merely resolve to private addresses are not caught here.

// Scheme-only check for the CDP bridge, which also serves the person's own
// tab-new: their URLs keep today's rules (private networks allowed).
export function assertHttpNavigationURL(raw: string): URL {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    throw browserError("INVALID_URL", "Navigation requires an explicit HTTP(S) URL");
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw browserError(
      "BROWSER_URL_FORBIDDEN",
      `Navigation scheme is not permitted: ${url.protocol}`,
    );
  }
  return url;
}

export function assertAgentNavigationURL(raw: string): URL {
  const url = assertHttpNavigationURL(raw);
  if (isPrivateNetworkHost(url.hostname)) {
    throw browserError(
      "BROWSER_URL_FORBIDDEN",
      "Agent browser navigation cannot reach private-network or link-local addresses",
    );
  }
  return url;
}

export function isPrivateNetworkHost(hostname: string): boolean {
  const host = hostname.replace(/^\[|\]$/g, "").toLowerCase();
  const v4 = parseIPv4(host);
  if (v4) return isPrivateIPv4(v4);
  if (host.includes(":")) return isPrivateIPv6(host);
  return false;
}

function parseIPv4(host: string): number[] | null {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host);
  if (!m) return null;
  const parts = m.slice(1).map(Number);
  return parts.every((p) => p <= 255) ? parts : null;
}

function isPrivateIPv4([a, b]: number[]): boolean {
  return (
    a === 0 ||
    a === 10 ||
    (a === 100 && b >= 64 && b <= 127) || // CGNAT, which Tailscale peers use
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && b === 168) ||
    (a === 169 && b === 254)
  );
}

function isPrivateIPv6(host: string): boolean {
  // WHATWG URL normalizes IPv4-mapped addresses to hex groups (::ffff:a9fe:a9fe).
  const mapped = /^::ffff:([0-9a-f]{1,4}):([0-9a-f]{1,4})$/.exec(host);
  if (mapped) {
    const hi = parseInt(mapped[1], 16);
    const lo = parseInt(mapped[2], 16);
    return isPrivateIPv4([hi >> 8, hi & 255, lo >> 8, lo & 255]);
  }
  if (host.startsWith("64:ff9b::")) return true; // NAT64 routes to any IPv4 address
  const first = host.split(":", 1)[0];
  if (first === "") return false; // ::1 and other :: forms
  const group = parseInt(first, 16);
  // fc00::/7 unique local, fe80::/10 link-local.
  return (group & 0xfe00) === 0xfc00 || (group & 0xffc0) === 0xfe80;
}

// For URLs an agent command supplies (tallpine browser open/tab-new). The phone remote
// uses normalizeAgentBrowserURL alone: it is the person navigating.
export function normalizeAgentCommandURL(input: string): string {
  const href = normalizeAgentBrowserURL(input);
  assertAgentNavigationURL(href);
  return href;
}
