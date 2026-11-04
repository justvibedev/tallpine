import { userFacingError } from "./connectionError";

export const ARCHIVE_FAILED_TITLE = "Couldn't archive the session";

/**
 * Runs an archive (kill) and reports a failure instead of dropping it.
 *
 * Both archive entry points — the board row and the chat screen — used to end
 * in `.catch(() => {})`, so a refused kill (offline machine, dirty-worktree
 * refusal, expired pairing) left the session on screen with no explanation.
 * Returns whether the archive succeeded.
 */
export async function archiveSession(
	kill: () => Promise<void>,
	report: (title: string, message: string) => void,
): Promise<boolean> {
	try {
		await kill();
		return true;
	} catch (cause) {
		report(ARCHIVE_FAILED_TITLE, userFacingError(cause, "Please try again."));
		return false;
	}
}
