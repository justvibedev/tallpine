import { unhighlightEntry } from "./args";
import { activeEntry } from "./entry-state";
import { agentURLResult, assertPanelPainted } from "./agent-results";
import { inputNativeArgs } from "./agent-action-neutral";
import type { AgentActionEnv } from "./agent-action-env";

export async function inputAction(env: AgentActionEnv) {
  const { session, action, args, runNative } = env;
  const entry = activeEntry(session);
  if (action === "unhighlight") return agentURLResult(await unhighlightEntry(entry));
  // Curved pointer movement needs the panel painting its frames.
  if ((action === "click" || action === "drag") && args.human === true) assertPanelPainted(session);
  return runNative(action, inputNativeArgs(action, args));
}
