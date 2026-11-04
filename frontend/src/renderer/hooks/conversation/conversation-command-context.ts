import type { QueryClient } from "@tanstack/react-query";

/** What the split-out mutation hooks share with useConversationCommands. */
export interface ConversationCommandContext {
	queryClient: QueryClient;
	sessionId: string | undefined;
	hostId: string | undefined;
	stateKey: (targetSessionId: string) => string;
	invalidate: () => void;
	refreshSessionInBackground: (targetSessionId: string) => void;
}
