import type { BrowserEntry, BrowserLogEntry, BrowserSessionEntry } from "./types";
import { DEFAULT_NETWORK_CAPTURE_SECONDS, MAX_EXTERNAL_TEXT_BYTES, MAX_NETWORK_CAPTURE_SECONDS, UNTRUSTED_BEGIN, UNTRUSTED_END } from "./constants";
import { sanitizeURLsInText } from "./url";
import { ensureDebugger } from "./network-capture";

export function objectValue(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

export function finiteNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value)
    ? value
    : undefined;
}

export async function unhighlightEntry(entry: BrowserEntry): Promise<unknown> {
  await ensureDebugger(entry);
  await entry.view.webContents.debugger.sendCommand("Overlay.enable");
  await entry.view.webContents.debugger.sendCommand("Overlay.hideHighlight");
  return { url: entry.view.webContents.getURL() };
}

export function stringArg(
  args: Record<string, unknown>,
  name: string,
  code: string,
  message: string,
  allowEmpty = false,
): string {
  const value = args[name];
  if (typeof value !== "string" || (!allowEmpty && !value.trim()))
    throw browserError(code, message);
  return value;
}

export function numberArg(value: unknown, min: number, max: number): number {
  if (typeof value !== "number" || !Number.isFinite(value)) return 0;
  return Math.max(min, Math.min(max, Math.round(value)));
}

export function networkDurationArg(value: unknown): number {
  if (value === undefined) return DEFAULT_NETWORK_CAPTURE_SECONDS;
  if (
    typeof value !== "number" ||
    !Number.isFinite(value) ||
    !Number.isInteger(value) ||
    value < 1 ||
    value > MAX_NETWORK_CAPTURE_SECONDS
  ) {
    throw browserError(
      "INVALID_ARGUMENT",
      `network capture duration must be an integer from 1 to ${MAX_NETWORK_CAPTURE_SECONDS} seconds`,
    );
  }
  return value;
}

export function externalText(
  value: unknown,
  maxBytes = MAX_EXTERNAL_TEXT_BYTES,
): string {
  const raw = value == null ? "" : String(value);
  const bytes = Buffer.from(raw, "utf8");
  if (bytes.length <= maxBytes) return raw;
  return `${bytes.subarray(0, maxBytes).toString("utf8")}\n[Content truncated at ${maxBytes} bytes]`;
}

function markUntrusted(value: string): string {
  const escaped = value
    .replaceAll(UNTRUSTED_BEGIN, `\\u003c${UNTRUSTED_BEGIN.slice(1)}`)
    .replaceAll(UNTRUSTED_END, `\\u003c${UNTRUSTED_END.slice(1)}`);
  return `${UNTRUSTED_BEGIN}\n${escaped}\n${UNTRUSTED_END}`;
}

export function browserSignalMessages(
  session: BrowserSessionEntry,
): BrowserLogEntry[] {
  return session.signals.entries.map((entry) => ({
    level: "error",
    message: markUntrusted(externalText(entry.message)),
    source:
      entry.kind === "console-error" ? "browser-console" : "browser-network",
    timestamp: entry.timestamp,
  }));
}

export function normalizeNativeMessages(
  result: Record<string, unknown>,
  action: string,
): {
  messages: BrowserLogEntry[];
  target: unknown;
  untrustedExternalContent: true;
} {
  const raw = Array.isArray(result.messages)
    ? result.messages
    : Array.isArray(result.value)
      ? result.value
      : [];
  const messages = raw.map((item): BrowserLogEntry => {
    if (typeof item === "string") {
      return {
        level: action === "errors" ? "error" : "log",
        message: markUntrusted(externalText(sanitizeURLsInText(item))),
        timestamp: new Date().toISOString(),
      };
    }
    const record =
      item && typeof item === "object" ? (item as Record<string, unknown>) : {};
    const level =
      typeof record.level === "string"
        ? record.level
        : typeof record.type === "string"
          ? record.type
          : action === "errors"
            ? "error"
            : "log";
    const message =
      typeof record.message === "string"
        ? record.message
        : typeof record.text === "string"
          ? record.text
          : JSON.stringify(record);
    return {
      level,
      message: markUntrusted(externalText(sanitizeURLsInText(message))),
      timestamp:
        typeof record.timestamp === "string"
          ? record.timestamp
          : new Date().toISOString(),
    };
  });
  return { messages, target: result.target, untrustedExternalContent: true };
}

export function throwIfAborted(signal?: AbortSignal): void {
  if (signal?.aborted)
    throw browserError(
      "BROWSER_COMMAND_CANCELED",
      "Browser command was canceled",
    );
}

export function abortableBrowserResult<T>(
  work: Promise<T>,
  signal?: AbortSignal,
): Promise<T> {
  if (!signal) return work;
  if (signal.aborted)
    return Promise.reject(
      browserError("BROWSER_COMMAND_CANCELED", "Browser command was canceled"),
    );
  return new Promise<T>((resolve, reject) => {
    const abort = () =>
      reject(
        browserError(
          "BROWSER_COMMAND_CANCELED",
          "Browser command was canceled",
        ),
      );
    signal.addEventListener("abort", abort, { once: true });
    work
      .then(resolve, reject)
      .finally(() => signal.removeEventListener("abort", abort));
  });
}

export function browserError(code: string, message: string): Error & { code: string } {
  return Object.assign(new Error(message), { code });
}
