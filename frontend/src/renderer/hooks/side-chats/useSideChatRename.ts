/* Side-chat tab rename state, split out of useSideChats (B303). */

import { useRef, useState } from "react";
import { apiErrorMessage } from "../../lib/api-client";
import { renameSideChat } from "../../lib/side-chats-api";
import type { Side } from "../../lib/side-chats-model";

export function useSideChatRename({
	sessionId, sides, setError, refreshList,
}: {
	sessionId: string;
	sides: Side[];
	setError: (message: string | undefined) => void;
	refreshList: () => Promise<unknown>;
}) {
	const [renameText, setRenameText] = useState<string>();
	const [renameId, setRenameId] = useState<string>();
	const renameCancelled = useRef(false);
	const beginRename = (side: Side) => {
		renameCancelled.current = false;
		setRenameId(side.id);
		setRenameText(side.label);
	};
	const saveRename = async () => {
		if (renameCancelled.current || !renameId) return;
		const id = renameId,
			label = renameText?.trim();
		setRenameId(undefined);
		setRenameText(undefined);
		if (!label || label === sides.find((side) => side.id === id)?.label) return;
		const { error: requestError } = await renameSideChat(sessionId, id, label);
		if (requestError) {
			setError(apiErrorMessage(requestError));
			return;
		}
		await refreshList();
	};
	return { renameText, setRenameText, renameId, setRenameId, renameCancelled, beginRename, saveRename };
}
