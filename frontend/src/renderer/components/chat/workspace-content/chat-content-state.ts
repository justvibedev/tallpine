import type { useChatContentData } from "./useChatContentData";
import type { useChatContentTabs } from "./useChatContentTabs";
import type { useChatContentStatus } from "./useChatContentStatus";

/** Merged state of the chat workspace hooks, handed to its section components. */
export type ChatContentState = ReturnType<typeof useChatContentData> &
	ReturnType<typeof useChatContentTabs> &
	ReturnType<typeof useChatContentStatus>;
