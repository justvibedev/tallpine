import { useRouter } from "expo-router";
import { useCallback } from "react";
import { haptics } from "../haptics";

type Known = object | null | undefined;

/**
 * The terminal has no overflow menu, so its artifacts get their own header
 * button, shown only once the agent has written something.
 */
export function useTerminalArtifacts({ known, shellOnly, sessionId, title, hostId }: { known: Known; shellOnly: boolean; sessionId: string; title?: string; hostId?: string }) {
	const router = useRouter();
	const hasArtifacts = !shellOnly && known != null && (known as { hasArtifacts?: boolean }).hasArtifacts === true;
	const openArtifacts = useCallback(() => {
		haptics.tap();
		router.push({ pathname: "/artifacts/[sessionId]", params: { sessionId, title, hostId } });
	}, [hostId, title, router, sessionId]);
	return { hasArtifacts, openArtifacts };
}
