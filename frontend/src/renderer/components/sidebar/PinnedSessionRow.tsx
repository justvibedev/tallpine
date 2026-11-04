import { useCallback, memo } from "react";
import { type WorkspaceSession } from "../../types/workspace";
import { SessionRow } from "./SessionRow";

export const PinnedSessionRow = memo(function PinnedSessionRow({
	session,
	active,
	hostLabel,
	onKilled,
	onOpenSession,
}: {
	session: WorkspaceSession;
	active: boolean;
	hostLabel?: string;
	onKilled?: (session: WorkspaceSession) => void;
	onOpenSession: (session: WorkspaceSession) => void;
}) {
	const onOpen = useCallback(() => onOpenSession(session), [onOpenSession, session]);
	return <SessionRow session={session} active={active} hostLabel={hostLabel} indented={false} onKilled={onKilled} onOpen={onOpen} />;
});
