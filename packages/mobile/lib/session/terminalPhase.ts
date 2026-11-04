import type { MuxStatus } from "../mux";
import type { Theme } from "../theme";

export const statusColorFor = (t: Theme): Record<MuxStatus, string> => ({
	connecting: t.amber,
	open: t.green,
	closed: t.textTertiary,
	error: t.red,
});

export function terminalInterfacePhaseLabel(phase?: string): string {
	switch (phase) {
		case "draining":
			return "Waiting for the current terminal turn to finish. New messages are queued safely.";
		case "source_stopping":
			return "Stopping the terminal controller before Chat starts.";
		case "source_stopped":
			return "Terminal controller stopped. The worktree and native conversation are unchanged.";
		case "target_starting":
			return "Resuming the same native conversation in Chat.";
		case "activating":
			return "Opening the Chat interface.";
		default:
			return "Checking that Chat can resume this agent's native conversation.";
	}
}
