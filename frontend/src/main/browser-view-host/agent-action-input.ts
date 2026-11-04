import { numberArg, stringArg, unhighlightEntry } from "./args";
import { activeEntry } from "./entry-state";
import { agentURLResult, assertPanelPainted } from "./agent-results";
import type { AgentActionEnv } from "./agent-action-env";

export async function inputAction(env: AgentActionEnv) {
  const { session, action, args, runNative } = env;
  const entry = activeEntry(session);
  switch (action) {
    case "click":
      if (args.human === true) assertPanelPainted(session);
      return runNative(action, {
        ref: stringArg(
          args,
          "ref",
          "REFERENCE_REQUIRED",
          "ref is required",
        ),
        ...(args.human === true ? { human: true } : {}),
      });
    case "dblclick":
    case "focus":
    case "hover":
    case "highlight":
    case "scrollintoview":
    case "check":
    case "uncheck":
      return runNative(action, {
        ref: stringArg(
          args,
          "ref",
          "REFERENCE_REQUIRED",
          "ref is required",
        ),
      });
    case "fill":
    case "type":
      return runNative(action, {
        ref: stringArg(
          args,
          "ref",
          "REFERENCE_REQUIRED",
          "ref is required",
        ),
        text: stringArg(
          args,
          "text",
          "INVALID_ARGUMENT",
          "text is required",
          true,
        ),
      });
    case "press":
      return runNative(action, {
        key: stringArg(
          args,
          "key",
          "INVALID_ARGUMENT",
          "key is required",
        ),
      });
    case "drag":
      if (args.human === true) assertPanelPainted(session);
      return runNative(action, {
        ref: stringArg(
          args,
          "ref",
          "REFERENCE_REQUIRED",
          "ref is required",
        ),
        targetRef: stringArg(
          args,
          "targetRef",
          "REFERENCE_REQUIRED",
          "target ref is required",
        ),
        ...(args.human === true ? { human: true } : {}),
      });
    case "unhighlight":
      return agentURLResult(await unhighlightEntry(entry));
    case "scroll":
      return runNative(action, {
        direction: stringArg(
          args,
          "direction",
          "INVALID_ARGUMENT",
          "direction is required",
        ),
        amount: numberArg(args.amount, 1, 5_000) || 600,
      });
    case "select":
      return runNative(action, {
        ref: stringArg(
          args,
          "ref",
          "REFERENCE_REQUIRED",
          "ref is required",
        ),
        value: stringArg(
          args,
          "value",
          "INVALID_ARGUMENT",
          "value is required",
          true,
        ),
      });
  }
  throw new Error(`Unhandled input action: ${action}`);
}
