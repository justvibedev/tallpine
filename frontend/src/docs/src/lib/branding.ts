export function visibleProductName(text: string): string {
  return text.replaceAll("Agent Orchestrator", "Tallpine").replace(/\bAO\b/g, "Tallpine");
}
