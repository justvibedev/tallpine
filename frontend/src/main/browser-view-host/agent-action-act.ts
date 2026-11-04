import { matchInstruction, type ActCandidate } from "../browser-act-matcher";
import { browserError, stringArg } from "./args";
import { actionFacts, actionPostcondition, actionPostconditionSatisfiedBeforeDispatch, observeActionPostcondition } from "./action-postconditions";
import { activeEntry } from "./entry-state";
import { isStaleReferenceError } from "./agent-results";
import { ACT_VERBS } from "./constants";
import type { AgentActionEnv } from "./agent-action-env";

async function performMatchedAction(
  env: AgentActionEnv,
  step: { verb: string; value: string | undefined; postcondition: ReturnType<typeof actionPostcondition>; dispatched: { current: boolean } },
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
  const instruction = stringArg(
    args,
    "instruction",
    "INVALID_ARGUMENT",
    "instruction is required",
  );
  const postcondition = actionPostcondition(args);
  const verb =
    typeof args.action === "string" && args.action.trim()
      ? args.action.trim()
      : "click";
  if (!ACT_VERBS.has(verb)) {
    throw browserError(
      "INVALID_ARGUMENT",
      `Unsupported act verb: ${verb}`,
    );
  }
  const value =
    verb === "fill" || verb === "type"
      ? stringArg(
          args,
          "value",
          "INVALID_ARGUMENT",
          "value is required",
          true,
        )
      : undefined;
  const nth =
    typeof args.nth === "number" && Number.isFinite(args.nth)
      ? Math.trunc(args.nth)
      : undefined;
  const snapshotOnce = async (): Promise<{
    text: string;
    refs: unknown;
  }> => {
    const result = await runNative("snapshot", { interactive: true });
    if (typeof result.snapshot !== "string") {
      throw browserError(
        "BROWSER_AUTOMATION_INVALID_OUTPUT",
        "Browser snapshot output was invalid",
      );
    }
    return { text: result.snapshot, refs: result.refs };
  };
  const unresolved = (
    outcome: "ambiguous" | "no-match",
    candidates: unknown,
    snapshot: string,
  ) => {
    // These outcomes hand the agent a tree of their own, so a later
    // delta must not be measured against the one it replaced.
    session.snapshotDeltaBaseline = undefined;
    return {
      outcome,
      instruction,
      ...(outcome === "ambiguous" ? { candidates } : {}),
      snapshot,
      untrustedExternalContent: true as const,
    };
  };
  const dispatched = { current: false };
  const step = { verb, value, postcondition, dispatched };

  const snapshot1 = await snapshotOnce();
  const match1 = matchInstruction(instruction, snapshot1.refs, {
    nth,
  });
  if (match1.outcome !== "matched")
    return unresolved(
      match1.outcome,
      "candidates" in match1 ? match1.candidates : undefined,
      snapshot1.text,
    );

  try {
    return await performMatchedAction(
      env,
      step,
      match1.candidate,
      false,
      snapshot1.text,
    );
  } catch (error) {
    // Element attributes/positions on real pages shift between the
    // snapshot that resolved a ref and the action that uses it, going
    // stale — this is the one case worth a single automatic retry
    // (re-snapshot, re-match the *original* instruction, try once
    // more) rather than making the agent notice and redo the whole
    // snapshot->click chain itself, which is the entire point of this
    // primitive. Any other failure, or a second stale ref, rethrows as
    // itself — an honest failure beats silently doing nothing on a
    // mutating action, and this must not become an unbounded loop
    // (mirrors ensureNativeActiveTab's "retry once, then surface
    // reality" convention elsewhere in this file).
    // A stale-ref failure is known to happen before dispatch. Once the
    // native action resolves, no observation failure may replay a
    // potentially non-idempotent action.
    if (dispatched.current || !isStaleReferenceError(error))
      throw error;
    const snapshot2 = await snapshotOnce();
    const match2 = matchInstruction(instruction, snapshot2.refs, {
      nth,
    });
    if (match2.outcome !== "matched") {
      return unresolved(
        match2.outcome,
        "candidates" in match2 ? match2.candidates : undefined,
        snapshot2.text,
      );
    }
    return performMatchedAction(
      env,
      step,
      match2.candidate,
      true,
      snapshot2.text,
    );
  }
}
