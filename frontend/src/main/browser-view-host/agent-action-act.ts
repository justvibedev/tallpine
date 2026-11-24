import type { ActCandidate } from "../browser-act-matcher";
import { actionFacts, actionPostconditionSatisfiedBeforeDispatch, observeActionPostcondition } from "./action-postconditions";
import { activeEntry } from "./entry-state";
import { actAction as neutralActAction, type ActStep } from "./agent-action-neutral";
import type { AgentActionEnv } from "./agent-action-env";

async function performMatchedAction(
  env: AgentActionEnv,
  step: ActStep,
  candidate: ActCandidate,
  retried: boolean,
  beforeSnapshot: string,
) {
  const { session, signal, runNative } = env;
  const { verb, value, postcondition, dispatched } = step;
  const actionEntry = activeEntry(session);
  const before = actionFacts(actionEntry);
  const generations = {
    documentGeneration: actionEntry.documentGeneration,
    navigationGeneration: actionEntry.navigationGeneration,
    blockedNavigationGeneration:
      actionEntry.blockedNavigationGeneration,
  };
  // URL/text expectations can already be true before the input is
  // dispatched. Capture that fact first so an immediately successful
  // native wait is not misreported as an application effect caused by
  // this action.
  const postconditionWasSatisfied = postcondition
    ? await actionPostconditionSatisfiedBeforeDispatch(
        postcondition,
        actionEntry,
        runNative,
      )
    : false;
  const result = await runNative(
    verb,
    (value !== undefined ? { ref: candidate.ref, text: value } : { ref: candidate.ref }),
  );
  dispatched.current = true;
  const observed = postcondition
    ? await observeActionPostcondition(
        postcondition,
        session,
        actionEntry,
        generations,
        beforeSnapshot,
        postconditionWasSatisfied,
        result,
        runNative,
        signal,
      )
    : undefined;
  const resultEntry = activeEntry(session);
  const navigation =
    resultEntry.tabId !== actionEntry.tabId ||
    actionEntry.navigationGeneration >
      generations.navigationGeneration
      ? { status: "observed" }
      : actionEntry.blockedNavigationGeneration >
          generations.blockedNavigationGeneration
        ? { status: "cancelled", reason: "beforeunload" }
        : { status: "not-observed" };
  return {
    // "matched" remains element-resolution status. inputDispatched and
    // postcondition report the distinct action/application outcomes.
    outcome: "matched",
    resolvedRef: candidate.ref,
    candidate,
    result,
    inputDispatched: true,
    before,
    after: actionFacts(resultEntry),
    navigation,
    ...(observed ? { postcondition: observed } : {}),
    retried,
    untrustedExternalContent: true,
  };
}

export async function actAction(env: AgentActionEnv) {
  const { session, args, runNative } = env;
  return neutralActAction(args, {
    runNative,
    perform: (step, candidate, retried, beforeSnapshot) =>
      performMatchedAction(env, step, candidate, retried, beforeSnapshot),
    invalidateBaseline: () => {
      session.snapshotDeltaBaseline = undefined;
    },
  });
}
