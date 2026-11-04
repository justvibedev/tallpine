import type { BrowserEntry, BrowserSessionEntry } from "./types";
import { activeEntry } from "./entry-state";
import { sanitizeBrowserURL } from "./url";
import { isAgentBrowserWaitTimeout } from "./agent-results";
import { browserError, throwIfAborted } from "./args";

type ActionPostcondition = {
  kind: "url" | "text" | "dialog" | "navigation" | "dom-change";
  value?: string;
  timeoutMs: number;
};

type ActionNativeRunner = (
  action: string,
  args?: Record<string, unknown>,
  signal?: AbortSignal,
) => Promise<Record<string, unknown>>;

const ACTION_POSTCONDITION_POLL_INTERVAL_MS = 200;

const ACTION_OBSERVATION_DEADLINE = Symbol("action-observation-deadline");

export function actionFacts(entry: BrowserEntry): Record<string, unknown> {
  return {
    tabId: entry.tabId,
    url: sanitizeBrowserURL(entry.view.webContents.getURL()),
    documentGeneration: entry.documentGeneration,
    navigationGeneration: entry.navigationGeneration,
  };
}

export function actionPostcondition(
  args: Record<string, unknown>,
): ActionPostcondition | undefined {
  if (args.postcondition === undefined) return undefined;
  if (
    !args.postcondition ||
    typeof args.postcondition !== "object" ||
    Array.isArray(args.postcondition)
  ) {
    throw browserError("INVALID_ARGUMENT", "postcondition must be an object");
  }
  const raw = args.postcondition as Record<string, unknown>;
  const kind = raw.kind;
  if (
    !["url", "text", "dialog", "navigation", "dom-change"].includes(
      String(kind),
    )
  ) {
    throw browserError(
      "INVALID_ARGUMENT",
      "postcondition.kind must be url, text, dialog, navigation, or dom-change",
    );
  }
  const value = typeof raw.value === "string" ? raw.value : undefined;
  if ((kind === "url" || kind === "text") && !value) {
    throw browserError(
      "INVALID_ARGUMENT",
      `postcondition ${kind} requires value`,
    );
  }
  if (
    raw.timeoutMs !== undefined &&
    (typeof raw.timeoutMs !== "number" ||
      !Number.isInteger(raw.timeoutMs) ||
      raw.timeoutMs < 1 ||
      raw.timeoutMs > 55_000)
  ) {
    throw browserError(
      "INVALID_ARGUMENT",
      "postcondition.timeoutMs must be an integer between 1 and 55000",
    );
  }
  const requestedTimeout =
    typeof raw.timeoutMs === "number" ? raw.timeoutMs : 10_000;
  return {
    kind: kind as ActionPostcondition["kind"],
    ...(value !== undefined ? { value } : {}),
    timeoutMs: requestedTimeout,
  };
}

function waitForActionPoll(
  signal: AbortSignal | undefined,
  timeoutMs: number,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(done, timeoutMs);
    function done() {
      signal?.removeEventListener("abort", aborted);
      resolve();
    }
    function aborted() {
      clearTimeout(timer);
      reject(
        browserError(
          "BROWSER_COMMAND_CANCELED",
          "Browser command was canceled",
        ),
      );
    }
    if (signal?.aborted) aborted();
    else signal?.addEventListener("abort", aborted, { once: true });
  });
}

export async function actionPostconditionSatisfiedBeforeDispatch(
  postcondition: ActionPostcondition,
  entry: BrowserEntry,
  runNative: ActionNativeRunner,
): Promise<boolean> {
  if (postcondition.kind === "url") {
    return entry.view.webContents.getURL().includes(postcondition.value!);
  }
  if (postcondition.kind !== "text") return false;
  const result = await runNative("get", { property: "text" });
  const text =
    typeof result.text === "string"
      ? result.text
      : typeof result.value === "string"
        ? result.value
        : undefined;
  if (text === undefined) {
    throw browserError(
      "BROWSER_AUTOMATION_INVALID_OUTPUT",
      "Browser text output was invalid",
    );
  }
  return text.includes(postcondition.value!);
}

async function runActionObservationUntil(
  deadline: number,
  runNative: ActionNativeRunner,
  action: string,
  args: Record<string, unknown>,
  signal?: AbortSignal,
): Promise<Record<string, unknown> | typeof ACTION_OBSERVATION_DEADLINE> {
  throwIfAborted(signal);
  const remaining = deadline - Date.now();
  if (remaining <= 0) return ACTION_OBSERVATION_DEADLINE;

  const controller = new AbortController();
  let deadlineReached = false;
  let timeout: ReturnType<typeof setTimeout> | undefined;
  let rejectExternalAbort: ((error: unknown) => void) | undefined;
  const externalAbort = new Promise<never>((_resolve, reject) => {
    rejectExternalAbort = reject;
  });
  const onExternalAbort = () => {
    controller.abort();
    rejectExternalAbort?.(
      browserError("BROWSER_COMMAND_CANCELED", "Browser command was canceled"),
    );
  };
  signal?.addEventListener("abort", onExternalAbort, { once: true });
  // AbortSignal does not replay an abort that raced with listener
  // registration. Close that narrow gap before starting provider I/O.
  if (signal?.aborted) {
    signal.removeEventListener("abort", onExternalAbort);
    throw browserError(
      "BROWSER_COMMAND_CANCELED",
      "Browser command was canceled",
    );
  }

  const operation = runNative(action, args, controller.signal);
  const internalDeadline = new Promise<typeof ACTION_OBSERVATION_DEADLINE>(
    (resolve) => {
      timeout = setTimeout(() => {
        deadlineReached = true;
        controller.abort();
        resolve(ACTION_OBSERVATION_DEADLINE);
      }, remaining);
    },
  );

  try {
    return await Promise.race([operation, internalDeadline, externalAbort]);
  } catch (error) {
    if (signal?.aborted)
      throw browserError(
        "BROWSER_COMMAND_CANCELED",
        "Browser command was canceled",
      );
    if (deadlineReached) return ACTION_OBSERVATION_DEADLINE;
    throw error;
  } finally {
    if (timeout) clearTimeout(timeout);
    signal?.removeEventListener("abort", onExternalAbort);
    // A provider that ignores cancellation must not produce an unhandled
    // rejection after the bounded caller has already returned. The real native
    // runner kills its process on this signal; this catch is the defensive edge.
    void operation.catch(() => undefined);
  }
}

export async function observeActionPostcondition(
  postcondition: ActionPostcondition,
  session: BrowserSessionEntry,
  sourceEntry: BrowserEntry,
  before: {
    documentGeneration: number;
    navigationGeneration: number;
    blockedNavigationGeneration: number;
  },
  beforeSnapshot: string,
  wasSatisfiedBeforeDispatch: boolean,
  actionResult: Record<string, unknown>,
  runNative: ActionNativeRunner,
  signal?: AbortSignal,
): Promise<Record<string, unknown>> {
  const deadline = Date.now() + postcondition.timeoutMs;
  const activeTabChanged = () =>
    activeEntry(session).tabId !== sourceEntry.tabId;
  const satisfiedNavigation = () =>
    activeTabChanged() ||
    sourceEntry.navigationGeneration > before.navigationGeneration;
  const cancelledNavigation = () =>
    !satisfiedNavigation() &&
    sourceEntry.blockedNavigationGeneration >
      before.blockedNavigationGeneration;
  const correlatedPageEffect = () =>
    satisfiedNavigation() ||
    sourceEntry.documentGeneration > before.documentGeneration;

  if (postcondition.kind === "url" || postcondition.kind === "text") {
    if (wasSatisfiedBeforeDispatch && !correlatedPageEffect()) {
      return {
        status: "already-satisfied",
        kind: postcondition.kind,
        expected: postcondition.value,
        reason: "pre-existing",
      };
    }
    try {
      const result = await runActionObservationUntil(
        deadline,
        runNative,
        "wait",
        {
          [postcondition.kind]: postcondition.value,
          timeoutMs: Math.max(1, deadline - Date.now()),
        },
        signal,
      );
      if (result === ACTION_OBSERVATION_DEADLINE) {
        return {
          status: cancelledNavigation() ? "cancelled" : "unmet",
          kind: postcondition.kind,
          expected: postcondition.value,
          reason: cancelledNavigation() ? "beforeunload" : "timeout",
        };
      }
      return {
        status: "satisfied",
        kind: postcondition.kind,
        expected: postcondition.value,
      };
    } catch (error) {
      throwIfAborted(signal);
      if (!isAgentBrowserWaitTimeout(error)) throw error;
      return {
        status: cancelledNavigation() ? "cancelled" : "unmet",
        kind: postcondition.kind,
        expected: postcondition.value,
        reason: cancelledNavigation() ? "beforeunload" : "timeout",
      };
    }
  }
  if (postcondition.kind === "dialog" && actionResult.dialogOpened === true) {
    return { status: "satisfied", kind: postcondition.kind };
  }

  for (;;) {
    throwIfAborted(signal);
    if (postcondition.kind === "navigation" && satisfiedNavigation()) {
      return { status: "satisfied", kind: postcondition.kind };
    }
    if (cancelledNavigation()) {
      return {
        status: "cancelled",
        kind: postcondition.kind,
        reason: "beforeunload",
      };
    }
    if (postcondition.kind === "dom-change") {
      if (sourceEntry.documentGeneration > before.documentGeneration) {
        return { status: "satisfied", kind: postcondition.kind };
      }
      // Compare like-for-like with act's interactive baseline. A full-page
      // snapshot would differ even when the DOM had not changed. Each process-
      // backed observation receives only the remaining postcondition budget.
      const snapshot = await runActionObservationUntil(
        deadline,
        runNative,
        "snapshot",
        { interactive: true },
        signal,
      );
      if (snapshot === ACTION_OBSERVATION_DEADLINE) {
        return { status: "unmet", kind: postcondition.kind, reason: "timeout" };
      }
      if (
        typeof snapshot.snapshot === "string" &&
        snapshot.snapshot !== beforeSnapshot
      ) {
        return { status: "satisfied", kind: postcondition.kind };
      }
    }
    if (postcondition.kind === "dialog") {
      const dialog = await runActionObservationUntil(
        deadline,
        runNative,
        "dialog",
        { operation: "status" },
        signal,
      );
      if (dialog === ACTION_OBSERVATION_DEADLINE) {
        return { status: "unmet", kind: postcondition.kind, reason: "timeout" };
      }
      if (
        dialog.open === true ||
        dialog.isOpen === true ||
        typeof dialog.type === "string"
      ) {
        return { status: "satisfied", kind: postcondition.kind, dialog };
      }
    }
    const remaining = deadline - Date.now();
    if (remaining <= 0)
      return { status: "unmet", kind: postcondition.kind, reason: "timeout" };
    await waitForActionPoll(
      signal,
      Math.min(ACTION_POSTCONDITION_POLL_INTERVAL_MS, remaining),
    );
  }
}
