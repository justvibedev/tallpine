/**
 * The owner's comments on a whole plan (B831), shared by the desktop and
 * mobile plan cards. Pure and structural: each app passes its own PlanView.
 */

export interface PlanCommentLike {
	outcome: string;
	approve?: boolean;
}

interface CommentsOf<C extends PlanCommentLike> {
	comments?: C[];
}

/** What the comments mean for the card. */
export type PlanCommentState =
	| "none" // no comment waits for the planner: the owner may approve
	| "considering" // the planner is answering a comment; approval waits for it
	| "held"; // the owner approved with a comment; it lands once the planner accepts

/** The comments the planner has not answered yet. */
export function pendingComments<C extends PlanCommentLike>(plan: CommentsOf<C>): C[] {
	return (plan.comments ?? []).filter((c) => c.outcome === "pending");
}

export function planCommentState(plan: CommentsOf<PlanCommentLike>): PlanCommentState {
	const pending = pendingComments(plan);
	if (pending.length === 0) return "none";
	return pending.some((c) => c.approve) ? "held" : "considering";
}

/** The request bodies: a blank comment is not sent. */
export function commentBody(comment: string | undefined): { comment?: string } {
	const text = comment?.trim() ?? "";
	return text ? { comment: text } : {};
}
