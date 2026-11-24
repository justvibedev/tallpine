/**
 * The backend-neutral CDP-direct page commands (B857): back, forward, reload,
 * eval and upload. Like viewport (agent-action-cdp.ts) they take the tab's
 * AgentBrowserDebugger, so the in-app browser and Chrome mode share one
 * implementation and result shape. They run on the host's own debugger session
 * rather than through agent-browser, whose CDP bridge refuses
 * DOM.setFileInputFiles (agent-browser-cdp-bridge.ts).
 */
import { basename, isAbsolute } from "node:path";
import type { AgentBrowserDebugger } from "../agent-browser-cdp-bridge";
import { browserError, stringArg } from "./args";
import type { NativeResult, NativeRunner } from "./agent-action-neutral";
import { ensureAgentDebugger } from "./network-capture";
import { sanitizeBrowserTitle, sanitizeBrowserURL } from "./url";

export const PAGE_CDP_ACTIONS: ReadonlySet<string> = new Set(["back", "forward", "reload", "eval", "upload"]);

/** Longest expression eval accepts, in characters. */
export const EVAL_MAX_EXPRESSION = 100_000;
/** Longest JSON eval returns, in characters; longer results are cut. */
export const EVAL_MAX_RESULT = 64 * 1024;
export const EVAL_DEFAULT_TIMEOUT_MS = 5_000;
export const EVAL_MAX_TIMEOUT_MS = 30_000;
/** Most files one upload sets. */
export const UPLOAD_MAX_FILES = 32;
/** How long upload waits for the clicked element to open a file chooser. */
export const FILE_CHOOSER_TIMEOUT_MS = 5_000;
/** How long back/forward/reload wait for the page to commit before returning. */
export const NAVIGATION_COMMIT_TIMEOUT_MS = 10_000;

/** What a page command needs from its host for the session's front tab. */
export type PageCdpDeps = {
  /** The tab's debugger and id; the tab the command acts on. */
  tab(): Promise<{ tabId: string; debug: AgentBrowserDebugger | undefined }>;
  runNative: NativeRunner;
  /** The target the result reports. */
  target(): unknown;
};

type Listener = (...args: unknown[]) => void;

/** Calls onEvent for the page's own debugger events until the returned stop runs. */
function listen(debug: AgentBrowserDebugger, onEvent: (method: string, params: Record<string, unknown>) => void): () => void {
  // Only the page's own session: child sessions (frames, workers, the native runtime's) are not ours.
  const listener: Listener = (_event, method, params, childSessionId) => {
    if (childSessionId || typeof method !== "string") return;
    onEvent(method, (params ?? {}) as Record<string, unknown>);
  };
  debug.on("message", listener);
  return () => {
    debug.off?.("message", listener);
  };
}

function timeout<T>(promise: Promise<T>, ms: number, onTimeout: () => T | Promise<T>): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const expired = new Promise<T>((resolve, reject) => {
    timer = setTimeout(() => {
      try {
        resolve(onTimeout());
      } catch (error) {
        reject(error);
      }
    }, ms);
  });
  return Promise.race([promise, expired]).finally(() => clearTimeout(timer));
}

type HistoryEntry = { id: number; url: string; title: string };

/**
 * back / forward / reload on the tab's own history, waiting (bounded) for the
 * main frame to commit so a following snapshot reads the new page.
 */
export async function historyAction(action: "back" | "forward" | "reload", deps: PageCdpDeps): Promise<Record<string, unknown>> {
  const { debug } = await deps.tab();
  const attached = await ensureAgentDebugger(debug);
  await attached.sendCommand("Page.enable");
  let entry: HistoryEntry | undefined;
  if (action !== "reload") {
    const history = (await attached.sendCommand("Page.getNavigationHistory")) as { currentIndex?: number; entries?: HistoryEntry[] };
    const index = (history.currentIndex ?? 0) + (action === "back" ? -1 : 1);
    entry = history.entries?.[index];
    if (!entry) {
      throw browserError("BROWSER_NO_HISTORY", action === "back" ? "There is no page to go back to" : "There is no page to go forward to");
    }
  }
  let committed!: () => void;
  const commit = new Promise<boolean>((resolve) => {
    committed = () => resolve(true);
  });
  const stop = listen(attached, (method, params) => {
    const frame = params.frame as { parentId?: unknown } | undefined;
    if ((method === "Page.frameNavigated" && frame && frame.parentId === undefined) || method === "Page.navigatedWithinDocument") committed();
  });
  try {
    if (entry) await attached.sendCommand("Page.navigateToHistoryEntry", { entryId: entry.id });
    else await attached.sendCommand("Page.reload");
    const done = await timeout(commit, NAVIGATION_COMMIT_TIMEOUT_MS, () => false);
    const now = (await attached.sendCommand("Page.getNavigationHistory").catch(() => undefined)) as
      | { currentIndex?: number; entries?: HistoryEntry[] }
      | undefined;
    const current = now?.entries?.[now.currentIndex ?? -1] ?? entry;
    return {
      url: sanitizeBrowserURL(current?.url || "about:blank"),
      title: sanitizeBrowserTitle(current?.title ?? ""),
      isLoading: !done,
      target: deps.target(),
    };
  } finally {
    stop();
  }
}

function evalTimeout(args: Record<string, unknown>): number {
  const value = args.timeoutMs;
  if (value === undefined) return EVAL_DEFAULT_TIMEOUT_MS;
  if (typeof value !== "number" || !Number.isFinite(value) || value < 1 || value > EVAL_MAX_TIMEOUT_MS) {
    throw browserError("INVALID_ARGUMENT", `timeoutMs must be between 1 and ${EVAL_MAX_TIMEOUT_MS}`);
  }
  return Math.round(value);
}

type RemoteObject = { type?: string; subtype?: string; value?: unknown; unserializableValue?: string; description?: string };
type EvaluateResponse = {
  result?: RemoteObject;
  exceptionDetails?: { text?: string; exception?: RemoteObject };
};

/**
 * eval: one expression in the page's main frame, by value. The expression and
 * the result are size-capped, and the page script is terminated at the
 * timeout (a promise that never settles fails the command at the same time).
 * Whether this tab may be evaluated in is the host's check (the login grant).
 */
export async function evalAction(args: Record<string, unknown>, deps: PageCdpDeps): Promise<Record<string, unknown>> {
  const expression = stringArg(args, "expression", "INVALID_ARGUMENT", "expression is required");
  if (expression.length > EVAL_MAX_EXPRESSION) {
    throw browserError("INVALID_ARGUMENT", `expression must be at most ${EVAL_MAX_EXPRESSION} characters`);
  }
  const timeoutMs = evalTimeout(args);
  const { debug } = await deps.tab();
  const attached = await ensureAgentDebugger(debug);
  const response = (await timeout(
    attached.sendCommand("Runtime.evaluate", {
      expression,
      returnByValue: true,
      awaitPromise: true,
      userGesture: false,
      timeout: timeoutMs,
    }),
    timeoutMs + 1_000,
    () => {
      throw browserError("BROWSER_EVAL_TIMEOUT", `eval did not finish within ${timeoutMs}ms`);
    },
  )) as EvaluateResponse;
  if (response.exceptionDetails) {
    const details = response.exceptionDetails;
    const message = details.exception?.description || details.text || "eval threw an exception";
    throw browserError("BROWSER_EVAL_FAILED", message.slice(0, 2_000));
  }
  const result = response.result ?? {};
  const value = result.unserializableValue ?? result.value;
  const json = value === undefined ? "undefined" : JSON.stringify(value) ?? "undefined";
  const truncated = json.length > EVAL_MAX_RESULT;
  return {
    type: result.subtype ?? result.type ?? "undefined",
    json: truncated ? json.slice(0, EVAL_MAX_RESULT) : json,
    ...(truncated ? { truncated: true, length: json.length } : { value: value ?? null }),
    untrustedExternalContent: true,
    target: deps.target(),
  };
}

function uploadPaths(args: Record<string, unknown>): string[] {
  const paths = args.paths;
  if (!Array.isArray(paths) || paths.length === 0) throw browserError("INVALID_ARGUMENT", "paths is required");
  if (paths.length > UPLOAD_MAX_FILES) throw browserError("INVALID_ARGUMENT", `upload accepts at most ${UPLOAD_MAX_FILES} files`);
  return paths.map((path) => {
    // The daemon resolves and limits the paths (worktree and session temp dir);
    // here only their shape is checked.
    if (typeof path !== "string" || !isAbsolute(path) || path.includes("\0")) {
      throw browserError("INVALID_ARGUMENT", "upload paths must be absolute");
    }
    return path;
  });
}

type Chooser = { backendNodeId?: unknown; mode?: unknown };

/**
 * upload <ref> <paths>: click the element with file chooser interception on, so
 * no native dialog opens on the person's screen, then hand the opened chooser
 * the files. Works for a file input and for a button that opens one.
 */
export async function uploadAction(args: Record<string, unknown>, deps: PageCdpDeps): Promise<Record<string, unknown>> {
  const ref = stringArg(args, "ref", "INVALID_ARGUMENT", "ref is required");
  const paths = uploadPaths(args);
  const { tabId, debug } = await deps.tab();
  const attached = await ensureAgentDebugger(debug);
  let opened!: (chooser: Chooser) => void;
  const chooser = new Promise<Chooser | undefined>((resolve) => {
    opened = resolve;
  });
  const stop = listen(attached, (method, params) => {
    if (method === "Page.fileChooserOpened") opened(params);
  });
  try {
    await attached.sendCommand("Page.enable");
    await attached.sendCommand("Page.setInterceptFileChooserDialog", { enabled: true });
    const clicked = await deps.runNative("click", { ref });
    const clickedTab = (clicked.target as { tabId?: unknown } | undefined)?.tabId;
    if (clickedTab !== undefined && clickedTab !== tabId) {
      throw browserError("BROWSER_TARGET_MISMATCH", `The click landed on tab ${String(clickedTab)}, not ${tabId}`);
    }
    const open = await timeout(chooser, FILE_CHOOSER_TIMEOUT_MS, () => undefined);
    if (!open) throw browserError("BROWSER_FILE_CHOOSER_NOT_OPENED", `Clicking ${ref} did not open a file chooser`);
    const backendNodeId = open.backendNodeId;
    if (typeof backendNodeId !== "number") throw browserError("BROWSER_FILE_CHOOSER_NOT_OPENED", "The file chooser has no input element");
    if (open.mode === "selectSingle" && paths.length > 1) {
      // Answer the chooser with nothing so it does not stay pending.
      await attached.sendCommand("DOM.setFileInputFiles", { files: [], backendNodeId }).catch(() => undefined);
      throw browserError("INVALID_ARGUMENT", "This file input accepts one file");
    }
    await attached.sendCommand("DOM.setFileInputFiles", { files: paths, backendNodeId });
    return {
      uploaded: paths.length,
      files: paths.map((path) => basename(path)),
      multiple: open.mode === "selectMultiple",
      target: clicked.target ?? deps.target(),
    };
  } finally {
    stop();
    await attached.sendCommand("Page.setInterceptFileChooserDialog", { enabled: false }).catch(() => undefined);
  }
}

/** Runs one PAGE_CDP_ACTIONS command. */
export function pageCdpAction(action: string, args: Record<string, unknown>, deps: PageCdpDeps): Promise<NativeResult> {
  switch (action) {
    case "back":
    case "forward":
    case "reload":
      return historyAction(action, deps);
    case "eval":
      return evalAction(args, deps);
    case "upload":
      return uploadAction(args, deps);
  }
  return Promise.reject(new Error(`Unhandled page CDP action: ${action}`));
}
