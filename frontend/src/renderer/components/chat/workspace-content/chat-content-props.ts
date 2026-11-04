import type { ChatDraftScope } from "../../../lib/chat-drafts";
import type { ChatWorkspaceProps } from "../ChatWorkspaceProps";

/** Props of ChatWorkspaceContent: the chat workspace props plus its draft scope. */
export type ChatWorkspaceContentProps = ChatWorkspaceProps & {
	draftScope: ChatDraftScope;
	draftPersistenceAvailable: boolean;
	onRetryDraftPersistence?: () => void;
};
