import { browserError, stringArg } from "./args";
import { pushNavState, readNavState } from "./nav-events";
import { normalizeAgentCommandURL } from "./agent-url-policy";
import { agentNavState, parseSnapshotDelta } from "./agent-results";
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
  const { session, action, args, runNative } = env;
  const interactive = Boolean(args.interactive);
  if (args.delta === true) {
    const tabId = session.activeTabId;
    const baseline = session.snapshotDeltaBaseline;
    const current =
      baseline &&
      baseline.tabId === tabId &&
      baseline.interactive === interactive
        ? baseline
        : undefined;
    const request = async (full: boolean) => {
      const result = await runNative(action, {
        interactive,
        delta: true,
        ...(full ? { full: true } : {}),
      });
      return {
        delta: parseSnapshotDelta(result.snapshot),
        boundary: result._boundary,
      };
    };
    let full = args.full === true || !current;
    let response = await request(full);
    if (
      !full &&
      response.delta.kind !== "full" &&
      response.delta.baseRevision !== current?.revision
    ) {
      full = true;
      response = await request(true);
    }
    if (full && response.delta.kind !== "full") {
      throw browserError(
        "BROWSER_AUTOMATION_INVALID_OUTPUT",
        "Browser snapshot delta output was invalid",
      );
    }
    // Anything that invalidated the baseline while this snapshot was in
    // flight (a background tab closing, a profile switch) must win over
    // a response captured before it.
    session.snapshotDeltaBaseline =
      session.activeTabId === tabId &&
      session.snapshotDeltaBaseline === baseline
        ? { tabId, interactive, revision: response.delta.revision }
        : undefined;
    return {
      ...response.delta,
      ...(response.boundary ? { _boundary: response.boundary } : {}),
      untrustedExternalContent: true,
    };
  }
  session.snapshotDeltaBaseline = undefined;
  const result = await runNative(action, { interactive });
  if (typeof result.snapshot !== "string") {
    throw browserError(
      "BROWSER_AUTOMATION_INVALID_OUTPUT",
      "Browser snapshot output was invalid",
    );
  }
  return {
    text: result.snapshot,
    refs: result.refs,
    ...(result._boundary ? { _boundary: result._boundary } : {}),
    target: result.target,
    untrustedExternalContent: true,
  };
}
