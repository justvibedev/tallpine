import { parseChangeRequestUrl } from "@tallpine/shared/format/change-request-url";
import { Bell, CheckCheck, CircleAlert, GitMerge, GitPullRequest, GitPullRequestArrow, GitPullRequestClosed, MessageSquareDiff, MessageSquareDot } from "lucide-react";
import { type NotificationDTO } from "../../lib/notifications";

type NotificationCopy = Pick<NotificationDTO, "body" | "title">;

export function notificationPRTitleLink(
	notification: NotificationDTO,
	title: string,
): { after: string; before: string; label: string; number: string; url: string } | null {
	const url = notification.target.kind === "pr" ? notification.target.prUrl : notification.prUrl;
	if (!url) return null;
	const match = /\bPR\s*#(\d+)\b/i.exec(title);
	if (!match || match.index === undefined) return null;
	return {
		before: title.slice(0, match.index),
		label: match[0],
		number: match[1],
		after: title.slice(match.index + match[0].length),
		url,
	};
}

export function notificationCopy(notification: NotificationDTO, sessionName?: string): NotificationCopy {
	if (notification.type !== "ready_to_merge") {
		return { title: notification.title, body: notification.body };
	}

	const session = sessionName?.trim();
	if (!session) {
		return { title: notification.title, body: notification.body };
	}

	const legacySessionTitle = `${session} is ready to merge`;
	const title =
		notification.title.trim().toLocaleLowerCase() === legacySessionTitle.toLocaleLowerCase()
			? readyNotificationFallbackTitle(notification)
			: notification.title;

	return {
		title,
		body: `PR from session ${session} is ready to merge. CI passed with no blocking review feedback.`,
	};
}

function readyNotificationFallbackTitle(notification: NotificationDTO): string {
	const titleNumber = notification.title.match(/\bPR\s*#(\d+)\b/i)?.[1];
	const urlNumber = parseChangeRequestUrl(notification.prUrl)?.number;
	const number = titleNumber ?? urlNumber;
	return number ? `PR #${number} is ready to merge` : "Pull request is ready to merge";
}

export function notificationMentions(notification: NotificationCopy, value: string): boolean {
	const needle = value.trim().toLocaleLowerCase();
	if (!needle) return false;
	return `${notification.title}\n${notification.body}`.toLocaleLowerCase().includes(needle);
}

export function notificationIcon(type: string) {
	switch (type) {
		case "needs_input":
			return MessageSquareDot;
		case "review_feedback":
			return MessageSquareDiff;
		case "ready_to_merge":
			return GitPullRequestArrow;
		case "agent_failed":
			return CircleAlert;
		case "agent_completed":
			return CheckCheck;
		case "pr_opened":
			return GitPullRequest;
		case "pr_merged":
			return GitMerge;
		case "pr_closed_unmerged":
			return GitPullRequestClosed;
		default:
			return Bell;
	}
}

// Bare colored glyph per type (no tile). Merged uses GitHub's PR-merged purple;
// the accent token is a near-black surface color in this theme and reads as
// invisible, so it must not be used for a glyph.
export function notificationIconClass(type: string): string {
	switch (type) {
		case "needs_input":
		case "review_feedback":
			return "text-warning";
		case "agent_failed":
			return "text-error";
		case "agent_completed":
		case "pr_opened":
		case "ready_to_merge":
			return "text-success";
		case "pr_merged":
			return "text-[#a371f7]";
		case "pr_closed_unmerged":
			return "text-error";
		default:
			return "text-muted-foreground";
	}
}
