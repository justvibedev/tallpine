/**
 * The backend-neutral CDP-direct agent browser actions: viewport emulation and
 * console/errors collection. They take the tab's AgentBrowserDebugger (an
 * Electron webContents debugger or a Chrome mode target, B849) so the in-app
 * browser and the Chrome host share one implementation and result shape.
 * Network capture lives in network-capture.ts on the same terms.
 */
import type { AgentBrowserDebugger } from "../agent-browser-cdp-bridge";
import { browserError, normalizeNativeMessages } from "./args";
import type { NativeRunner } from "./agent-action-neutral";
import { ensureAgentDebugger } from "./network-capture";
import type { BrowserLogEntry } from "./network-types";

export type ViewportResult =
  | { reset: true }
  | { width: number; height: number; deviceScaleFactor: number; mobile: boolean };

function dimension(args: Record<string, unknown>, name: string): number {
  const value = args[name];
  if (typeof value !== "number" || !Number.isFinite(value) || value < 1) {
    throw browserError("INVALID_ARGUMENT", `${name} must be a number >= 1`);
  }
  return Math.min(10000, Math.round(value));
}

/** The device metrics a viewport command asks for, validated; null means reset. */
export function viewportMetrics(args: Record<string, unknown>): Exclude<ViewportResult, { reset: true }> | null {
  if (args.reset === true) return null;
  const width = dimension(args, "width");
  const height = dimension(args, "height");
  const dpr = args.deviceScaleFactor;
  if (dpr !== undefined && (typeof dpr !== "number" || !Number.isFinite(dpr) || dpr < 0)) {
    throw browserError("INVALID_ARGUMENT", "deviceScaleFactor must be a non-negative number");
  }
  return { width, height, deviceScaleFactor: typeof dpr === "number" ? Math.min(8, dpr) : 0, mobile: args.mobile === true };
}

/**
 * Device-metrics emulation on the agent's page, so screenshots and layout use
 * a fixed width regardless of the window's own size. It is per tab and lasts
 * until reset (or the debugger detaches). Arguments are validated before the
 * debugger is touched.
 */
export async function applyViewport(
  debug: () => Promise<AgentBrowserDebugger | undefined>,
  args: Record<string, unknown>,
): Promise<ViewportResult> {
  const metrics = viewportMetrics(args);
  const attached = await ensureAgentDebugger(await debug());
  if (!metrics) {
    await attached.sendCommand("Emulation.clearDeviceMetricsOverride");
    return { reset: true };
  }
  await attached.sendCommand("Emulation.setDeviceMetricsOverride", metrics);
  return metrics;
}

/**
 * console / errors: the native runtime's page messages, normalized, plus the
 * host's own error signals for errors (load failures it saw outside the page).
 */
export async function messagesAction(
  action: "console" | "errors",
  runNative: NativeRunner,
  hostSignals: () => BrowserLogEntry[] = () => [],
): Promise<ReturnType<typeof normalizeNativeMessages>> {
  const native = normalizeNativeMessages(await runNative(action), action);
  if (action === "console") return native;
  return { ...native, messages: [...native.messages, ...hostSignals()] };
}
