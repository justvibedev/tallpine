import type { WorkspaceSession } from "../../types/workspace";
import { sortedPRs } from "../../types/workspace";

export function reviewsTabVisible(session: WorkspaceSession | undefined): boolean {
	if (!session) return true;
	return sortedPRs(session).some((pr) => pr.state === "open" || pr.state === "draft");
}

export function externalReviewActorMatchesPRAuthor(actor: string | undefined, author: string | undefined): boolean {
	const normalizedActor = normalizeReviewerId(actor);
	const normalizedAuthor = normalizeReviewerId(author);
	return normalizedActor !== "" && normalizedActor === normalizedAuthor;
}

export function normalizeReviewerId(value: string | undefined): string {
	return value?.trim().replace(/^@+/, "").toLowerCase() ?? "";
}
