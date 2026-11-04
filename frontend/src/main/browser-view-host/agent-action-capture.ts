import { browserError, browserSignalMessages, networkDurationArg, normalizeNativeMessages } from "./args";
import { activeEntry } from "./entry-state";
import { agentActionTarget, isAgentBrowserTimeout } from "./agent-results";
import { MAX_SCREENSHOT_BYTES } from "./constants";
import { clearNetworkCapture, networkCaptureResult, networkCaptureStatus, networkEntryFor, startNetworkCapture, stopNetworkCapture } from "./network-capture";
import type { AgentActionEnv } from "./agent-action-env";

export async function captureAction(env: AgentActionEnv) {
  const { ctx, sessionId, session, signal, action, args, runNative } = env;
  const { options } = ctx;
  const { agentBrowserTargets, captureScreenshotFallback, ensureNativeActiveTab, queueNativeOperation, redirectToAgentTab } = ctx.fns;
  const entry = activeEntry(session);
  switch (action) {
    case "screenshot":
      if (!options.agentBrowserRuntime) {
        throw browserError(
          "BROWSER_AUTOMATION_UNAVAILABLE",
          "Browser automation runtime is unavailable",
        );
      }
      return queueNativeOperation(
        session,
        async () => {
          await ensureNativeActiveTab(session, signal);
          await redirectToAgentTab(session, signal);
          const targetEntry = activeEntry(session);
          await targetEntry.ready;
          try {
            const result = await options.agentBrowserRuntime!.screenshot(
              sessionId,
              agentBrowserTargets(session),
              signal,
              { annotate: args.annotate === true },
            );
            return { ...result, target: agentActionTarget(targetEntry) };
          } catch (error) {
            if (!isAgentBrowserTimeout(error)) throw error;
            const image = await captureScreenshotFallback(targetEntry, signal);
            if (image.isEmpty()) {
              throw browserError("SCREENSHOT_UNAVAILABLE", "The browser page could not be captured");
            }
            const png = image.toPNG();
            if (png.length > MAX_SCREENSHOT_BYTES) {
              throw browserError("SCREENSHOT_UNAVAILABLE", "Browser screenshot exceeded Tallpine's size limit");
            }
            const { width, height } = image.getSize();
            return {
              data: png.toString("base64"),
              width,
              height,
              target: agentActionTarget(targetEntry),
              untrustedExternalContent: true as const,
            };
          }
        },
        signal,
      );
    case "network-start":
      return startNetworkCapture(
        session,
        entry,
        networkDurationArg(args.durationSeconds),
      );
    case "network-status":
      return networkCaptureStatus(networkEntryFor(session));
    case "network-list":
      return networkCaptureResult(networkEntryFor(session));
    case "network-stop":
      return stopNetworkCapture(networkEntryFor(session), "stopped");
    case "network-clear":
      return clearNetworkCapture(networkEntryFor(session));
    case "console":
      return normalizeNativeMessages(await runNative(action), action);
    case "errors": {
      const native = normalizeNativeMessages(
        await runNative(action),
        action,
      );
      return {
        ...native,
        messages: [...native.messages, ...browserSignalMessages(session)],
      };
    }
  }
  throw new Error(`Unhandled capture action: ${action}`);
}
