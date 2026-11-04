// The prompt Tallpine sends the orchestrator to name a new task, and the `session rename`
// command it answers with. Desktop and phone draw the pair as one "task named" line.

const TITLE_UPDATE = "Tallpine TASK TITLE UPDATE";
const RENAME_COMMAND = /\b(?:tallpine|tp)'?\s+session\s+rename\s+\S+\s+(["'])(.+?)\1/;

/** The parts of a conversation item these readers look at. */
export interface TitleUpdateItem {
	kind: string;
	role?: string;
	origin?: string;
	text?: string;
	activityKind?: string;
	summary?: string;
	detail?: { command?: string } | null;
}

export function isTaskTitleUpdate(item: TitleUpdateItem): boolean {
	return item.kind === "message" && item.role === "user" && item.origin !== "human" && (item.text ?? "").startsWith(TITLE_UPDATE);
}

/** The title a turn's `session rename` command gave the task, if it ran one. */
export function renamedTitle(items: readonly TitleUpdateItem[]): string | undefined {
	for (const item of items) {
		if (item.kind !== "activity" || item.activityKind !== "command") continue;
		const match = RENAME_COMMAND.exec(item.detail?.command ?? item.summary ?? "");
		if (match) return match[2];
	}
	return undefined;
}
