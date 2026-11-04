import { browserError } from "./args";
import { activeEntry } from "./entry-state";
import type { BrowserSessionEntry } from "./types";
import { agentActionTarget, isAgentBrowserCommandFailure } from "./agent-results";
import type { HostContext } from "./context";

export function createRunNative(
  ctx: HostContext,
  sessionId: string,
  session: BrowserSessionEntry,
  signal: AbortSignal | undefined,
) {
  const { options } = ctx;
  const { activateTab, agentBrowserTargets, ensureNativeActiveTab, queueNativeOperation, redirectToAgentTab } = ctx.fns;

        const runNative = async (
          nativeAction: string,
          nativeArgs: Record<string, unknown> = {},
          nativeSignal: AbortSignal | undefined = signal,
        ): Promise<
          Record<string, unknown> & {
            target: { tabId: string; url: string; origin: string };
          }
        > => {
          if (!options.agentBrowserRuntime) {
            throw browserError(
              "BROWSER_AUTOMATION_UNAVAILABLE",
              "Browser automation runtime is unavailable",
            );
          }
          return queueNativeOperation(
            session,
            async () => {
              await ensureNativeActiveTab(session, nativeSignal, nativeAction.startsWith("tab-"));
              if (!nativeAction.startsWith("tab-")) {
                // The person may have switched to their own tab while this was
                // queued; move to an agent tab before targeting the active one.
                await redirectToAgentTab(session, nativeSignal);
              }
              const targetEntry = activeEntry(session);
              const targetTabId = targetEntry.tabId;
              await targetEntry.ready;
              const targetBeforeAction = agentActionTarget(targetEntry);
              const activationRevision = session.activationRevision;
              let result: Record<string, unknown>;
              if (nativeAction === "tab-select") {
                // A failed selection may still have changed the native target.
                // Require fresh synchronization before the next page action.
                session.nativeActiveTabId = undefined;
              }
              try {
                session.nativeCommandOrigin = "agent";
                try {
                  result = await options.agentBrowserRuntime!.runAction(
                    sessionId,
                    nativeAction,
                    nativeArgs,
                    agentBrowserTargets(session),
                    nativeSignal,
                  );
                } finally {
                  session.nativeCommandOrigin = undefined;
                }
              } catch (error) {
                if (
                  nativeAction === "tab-select" &&
                  isAgentBrowserCommandFailure(error)
                ) {
                  const requestedTabId =
                    typeof nativeArgs.tabId === "string"
                      ? nativeArgs.tabId
                      : targetTabId;
                  throw browserError(
                    "BROWSER_TARGET_MISMATCH",
                    `Browser automation could not target Tallpine tab ${requestedTabId}`,
                  );
                }
                throw error;
              }
              if (
                nativeAction === "tab-select" &&
                typeof nativeArgs.tabId === "string"
              ) {
                session.nativeActiveTabId = nativeArgs.tabId;
                const requestedEntry = session.tabs.get(nativeArgs.tabId);
                // An in-flight command must not overwrite a newer human selection,
                // even if they switched away and back to the original tab.
                if (
                  session.activationRevision === activationRevision &&
                  requestedEntry && requestedEntry.agentTab === undefined
                ) {
                  activateTab(session, nativeArgs.tabId);
                }
              }
              if (nativeAction === "tab-new" || nativeAction === "tab-close") {
                session.nativeActiveTabId = undefined;
              }
              if (nativeAction.startsWith("tab-") || nativeAction === "frame")
                session.snapshotDeltaBaseline = undefined;
              if (nativeAction.startsWith("tab-"))
                await ensureNativeActiveTab(session, nativeSignal, true);
              const targetAfterAction = session.tabs.get(targetTabId);
              return {
                ...result,
                target: targetAfterAction
                  ? agentActionTarget(targetAfterAction)
                  : targetBeforeAction,
              };
            },
            nativeSignal,
          );
        };

  return runNative;
}
