import type { ReactNode } from "react";
import { startingConversationSnapshot } from "./OrchestratorStartingChat";
import type { ConversationSnapshot } from "../../types/conversation";
import type { WorkspaceSession } from "../../types/workspace";

export function unavailableConversationSnapshot(session: WorkspaceSession): ConversationSnapshot {
	return {
		...startingConversationSnapshot(session.id, session.provider),
		controller: { state: "stopped", error: "Conversation unavailable" },
	};
}

export function Centered({ children }: { children: ReactNode }) {
	return (
		<div className="flex h-full flex-col items-center justify-center gap-2 bg-background px-6">
			{children}
		</div>
	);
}
