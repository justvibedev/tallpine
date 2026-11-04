import type { BrowserDevToolsPlacement, BrowserRect } from "./types";

// Hidden targets still need a real viewport for screenshots, responsive
// layout, scrolling, and pointer automation before the panel is first shown.
export const OFFSCREEN_BOUNDS: BrowserRect = {
  x: -10_000,
  y: -10_000,
  width: 1280,
  height: 720,
};

// Must match `--radius-lg` (tokens.css, 0.625rem = 10px) — `.browser-panel`'s own
// `rounded-lg` corner. The native view isn't a DOM node, so CSS never clips it;
// this is the only thing rounding its corners. A mismatch here leaves a sliver of
// the page's own background peeking past the DOM panel's rounded corner curve.
export const BROWSER_VIEW_BORDER_RADIUS = 10;

export const DEFAULT_NETWORK_CAPTURE_SECONDS = 60;

export const MAX_NETWORK_CAPTURE_SECONDS = 300;

export const MAX_NETWORK_REQUESTS = 200;

// Keep a bounded, metadata-only history for explicit errors queries. Browser
// failures must never be pushed into an agent's live conversation on their own.
export const MAX_BROWSER_SIGNALS = MAX_NETWORK_REQUESTS;

export const MAX_BROWSER_SIGNAL_BYTES = 16 * 1024;

export const FAVICON_SIZE = 32;

export const MAX_FAVICON_BYTES = 256 * 1024;

export const FAVICON_REQUEST_TIMEOUT_MS = 5_000;

export const DEFAULT_NATIVE_DEVTOOLS_PLACEMENT: BrowserDevToolsPlacement = "right";

export const MAX_EXTERNAL_TEXT_BYTES = 1 << 20;

// Popup activation happens only after an already-warm automation session has
// dispatched an action. It must never inherit agent-browser's 60 second command
// timeout and hold every later command behind the per-session queue.
export const POPUP_TARGET_SYNC_TIMEOUT_MS = 5_000;

// Annotation submit must never feel laggy: capture is best-effort and bounded
// so a slow/hung capturePage() can't delay the send past this ceiling.
export const ANNOTATION_SNAPSHOT_TIMEOUT_MS = 200;

export const MAX_SCREENSHOT_BYTES = 5 << 20;

// A native screenshot may time out while the hidden WebContentsView remains
// capturable through Electron. Keep this fallback bounded and cancelable.
export const SCREENSHOT_FALLBACK_TIMEOUT_MS = 5_000;

// Caps the longest edge so the encoded image stays small and matches Claude
// vision's effective resolution — larger just costs more tokens for no gain.
export const ANNOTATION_SNAPSHOT_MAX_DIMENSION = 1568;

export const UNTRUSTED_BEGIN = "<<<BEGIN UNTRUSTED EXTERNAL CONTENT>>>";

export const UNTRUSTED_END = "<<<END UNTRUSTED EXTERNAL CONTENT>>>";

// Browser targets are shared with session automation after navigation. Keep
// local files out of this surface even when navigation starts in the human
// address bar; workspace files arrive through the daemon's confined HTTP
// preview origin instead.
export const ALLOWED_PROTOCOLS = new Set(["http:", "https:"]);

export const TEMPORARY_BROWSER_PARTITION_PREFIX = "persist:ao-browser-temporary-";

// A confirmed profile switch refuses new agent commands and waits for the
// in-flight ones (and stopped page loads) to finish. Agent commands can run for
// up to 60s, so without a cap the switch would hang and block every other agent
// command that long. After this, it fails with "still busy" and the user retries.
export const PROFILE_SWITCH_SETTLE_MS = 10_000;

// A remote session's previews show on Tallpine's own preview origins (remote-proxy.ts),
// as the renderer's showPreview matches them.
export const REMOTE_PREVIEW_URL = /^https:\/\/ao-preview-[0-9a-f]{32}\.localhost(?::\d+)?\//;

// The `{ref[, text]}`-shaped action family "act" can resolve a target for and
// then perform, matching every case in the switch above that only ever needs a
// ref (or a ref plus text). "drag" (needs two independently-matched targets)
// and "select" (needs matching an option's text within a resolved control, not
// just the control) are harder matching problems and are deliberately excluded
// from v1.
export const ACT_VERBS = new Set([
  "click",
  "dblclick",
  "focus",
  "hover",
  "fill",
  "type",
  "check",
  "uncheck",
]);
