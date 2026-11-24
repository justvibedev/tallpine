import { browserError, stringArg } from "./args";
import { pushNavState, readNavState } from "./nav-events";
import { normalizeAgentCommandURL } from "./agent-url-policy";
import { agentNavState } from "./agent-results";
import { snapshotAction as neutralSnapshotAction } from "./agent-action-neutral";
import type { AgentActionEnv } from "./agent-action-env";

export async function openAction(env: AgentActionEnv) {
  const { ctx, session, action, args, runNative } = env;
  const { options } = ctx;
  const url = stringArg(
    args,
    "url",
    "URL_REQUIRED",
    "url is required",
  );
  const result = await runNative(action, {
    url: normalizeAgentCommandURL(url),
  });
  const targetEntry = session.tabs.get(result.target.tabId);
  if (!targetEntry) {
    throw browserError(
      "BROWSER_TARGET_UNAVAILABLE",
      `Browser target Tallpine tab ${result.target.tabId} is unavailable`,
    );
  }
  const navState =
    targetEntry.tabId === session.activeTabId
      ? pushNavState(options, targetEntry)
      : readNavState(targetEntry);
  return { ...agentNavState(navState), target: result.target };
}

export async function snapshotAction(env: AgentActionEnv) {
  const { session, args, runNative } = env;
  return neutralSnapshotAction(args, {
    activeTabId: () => session.activeTabId,
    baseline: () => session.snapshotDeltaBaseline,
    setBaseline: (baseline) => {
      session.snapshotDeltaBaseline = baseline;
    },
  }, runNative);
}
