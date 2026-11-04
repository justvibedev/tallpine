import { browserError, stringArg } from "./args";
import { sanitizeBrowserTitle, sanitizeBrowserURL } from "./url";
import { activeEntry } from "./entry-state";
import type { BrowserDevToolsInput } from "./types";
import { agentActionTarget } from "./agent-results";
import { ensureDebugger } from "./network-capture";
import type { AgentActionEnv } from "./agent-action-env";

export async function pageAction(env: AgentActionEnv) {
  const { ctx, session, action, args, runNative } = env;
  const { agentViewportOverrides, liveViewportOverrides, devtoolsAction } = ctx.fns;
  const entry = activeEntry(session);
  switch (action) {
    case "get": {
      const property = stringArg(
        args,
        "property",
        "INVALID_ARGUMENT",
        "property is required",
      );
      const result = await runNative(action, {
        property,
        ref:
          typeof args.ref === "string" && args.ref.trim()
            ? args.ref
            : undefined,
      });
      const value = result.value ?? result[property];
      const safeValue =
        property === "url"
          ? sanitizeBrowserURL(String(value ?? ""))
          : property === "title"
            ? sanitizeBrowserTitle(String(value ?? ""))
            : value;
      return {
        ...result,
        ...(property === "url" || property === "title"
          ? { [property]: safeValue }
          : {}),
        value: safeValue,
      };
    }
    case "wait":
      return runNative(action, args);
    case "frame":
    case "dialog":
      return runNative(action, args);
    case "devtools-open":
    case "devtools-close": {
      const operation = action.slice(
        "devtools-".length,
      ) as BrowserDevToolsInput["operation"];
      return devtoolsAction(session, operation);
    }
    case "viewport": {
      // Device-metrics emulation on the agent's page, so screenshots and
      // layout use a fixed width regardless of the panel's own size. It
      // is per tab and lasts until reset (or the debugger detaches).
      await ensureDebugger(entry);
      const debug = entry.view.webContents.debugger;
      if (args.reset === true) {
        agentViewportOverrides.delete(entry);
        liveViewportOverrides.delete(entry);
        await debug.sendCommand("Emulation.clearDeviceMetricsOverride");
        // Hand the slot back to the live-stream pin if a parked stream needs it.
        ctx.fns.syncLiveViewport(session, entry);
        return { reset: true, target: agentActionTarget(entry) };
      }
      const dimension = (name: string): number => {
        const value = args[name];
        if (typeof value !== "number" || !Number.isFinite(value) || value < 1) {
          throw browserError("INVALID_ARGUMENT", `${name} must be a number >= 1`);
        }
        return Math.min(10000, Math.round(value));
      };
      const width = dimension("width");
      const height = dimension("height");
      const dpr = args.deviceScaleFactor;
      if (dpr !== undefined && (typeof dpr !== "number" || !Number.isFinite(dpr) || dpr < 0)) {
        throw browserError("INVALID_ARGUMENT", "deviceScaleFactor must be a non-negative number");
      }
      const deviceScaleFactor = typeof dpr === "number" ? Math.min(8, dpr) : 0;
      const mobile = args.mobile === true;
      await debug.sendCommand("Emulation.setDeviceMetricsOverride", {
        width,
        height,
        deviceScaleFactor,
        mobile,
      });
      agentViewportOverrides.add(entry);
      return { width, height, deviceScaleFactor, mobile, target: agentActionTarget(entry) };
    }
  }
  throw new Error(`Unhandled page action: ${action}`);
}
