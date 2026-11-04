/** Workspace request error and retry policy. */
import { apiErrorCode, apiErrorMessage } from "../../lib/api-client";

/** A failed workspace read that keeps the daemon's error code, for callers that act on it. */
export class WorkspaceRequestError extends Error {
	constructor(
		message: string,
		readonly code?: string,
		readonly status?: number,
	) {
		super(message);
		this.name = "WorkspaceRequestError";
	}
}

export function workspaceRequestError(error: unknown, response: Response | undefined, fallback: string): WorkspaceRequestError {
	return new WorkspaceRequestError(apiErrorMessage(error, fallback), apiErrorCode(error), response?.status);
}

export function workspaceErrorCode(error: unknown): string | undefined {
	return error instanceof WorkspaceRequestError ? error.code : undefined;
}

/**
 * Retries a failed read up to three times, except a 4xx answer: it comes back
 * the same every time, and the recovery keyed on its code (a moved tip, a gone
 * branch) would wait out the backoff.
 */
export function retryWorkspaceRead(failureCount: number, error: Error): boolean {
	const status = error instanceof WorkspaceRequestError ? error.status : undefined;
	return failureCount < 3 && !(status !== undefined && status >= 400 && status < 500);
}
