import type { BrowserSessionEntry } from "./types";
import type { HostContext } from "./context";
import type { createRunNative } from "./agent-run-native";

// What one agent browser action handler needs from execute().
export type AgentActionEnv = {
  ctx: HostContext;
  sessionId: string;
  session: BrowserSessionEntry;
  signal: AbortSignal | undefined;
  action: string;
  args: Record<string, unknown>;
  runNative: ReturnType<typeof createRunNative>;
};
