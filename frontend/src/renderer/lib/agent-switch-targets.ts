// Agents a Chat session can switch to: the ones with a structured Chat driver.
export const CHAT_SWITCH_TARGETS = ["claude-code", "codex", "deepseek-harness"] as const;
// Agents a Terminal session can switch to: the Chat agents that also run in a terminal, plus
// terminal-only agents that take the handoff note on their command line.
export const TERMINAL_SWITCH_TARGETS = [...CHAT_SWITCH_TARGETS, "kiro"] as const;
// Terminal targets that answer one task and exit: greyed out with the reason there, still switchable in Chat.
export const TERMINAL_ONE_SHOT_TARGETS: ReadonlySet<string> = new Set(["deepseek-harness"]);
