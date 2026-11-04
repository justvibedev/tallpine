import { useCallback } from "react";
import { LOCAL_HOST, type HostId } from "../../lib/hosts";
import { type NotificationDTO } from "../../lib/notifications";
import { useNavigateToSession } from "../../lib/navigate-to-session";

export function useNotificationTargetNavigation() {
	const navigateToSession = useNavigateToSession();
	const openSession = useCallback(
		(notification: NotificationDTO, hostId: HostId = LOCAL_HOST) => {
			const sessionId = notification.target.sessionId || notification.sessionId;
			if (!sessionId) return;
			navigateToSession(notification.projectId, sessionId, hostId);
		},
		[navigateToSession],
	);

	const openPrimary = useCallback(
		(notification: NotificationDTO, hostId: HostId = LOCAL_HOST) => {
			if (notification.target.kind === "pr" && notification.target.prUrl) {
				window.open(notification.target.prUrl, "_blank", "noopener,noreferrer");
				return;
			}
			openSession(notification, hostId);
		},
		[openSession],
	);

	return { openPrimary, openSession };
}
