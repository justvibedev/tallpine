import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import type { NotificationDTO } from "../../lib/notifications";
import { NotificationCenter } from "../NotificationCenter";

/** The Inbox's left panel: the notification list; picking a row shows it in the center. */
export function InboxList() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const select = (notification: NotificationDTO) => void navigate({ to: "/inbox", search: { item: notification.id } });
	return (
		<section aria-label={t("notify.title")} className="flex min-h-0 flex-1 flex-col" data-testid="inbox-list">
			<NotificationCenter active onSelect={select} />
		</section>
	);
}
