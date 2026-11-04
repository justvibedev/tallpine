import type { components } from "@tallpine/shared/api/schema";
import type { ServerConfig } from "../config";
import { API, req } from "./http";

// ---- Notification history ---------------------------------------------------

export type NotificationType = components["schemas"]["NotificationResponse"]["type"];

export type NotificationRecord = components["schemas"]["NotificationResponse"];

export type NotificationPage = {
	notifications: NotificationRecord[];
	nextCursor?: string;
	unreadCount: number;
};

// History behind the Settings → Notifications row. Push only ever surfaces the
// notifications that arrive while the phone is reachable; this is the durable
// record the daemon keeps either way.
export async function getNotifications(
	cfg: ServerConfig,
	opts: { status?: "unread" | "all"; limit?: number; cursor?: string } = {},
	init?: Pick<RequestInit, "signal">,
): Promise<NotificationPage> {
	const qs = new URLSearchParams();
	if (opts.status) qs.set("status", opts.status);
	if (opts.limit) qs.set("limit", String(opts.limit));
	if (opts.cursor) qs.set("cursor", opts.cursor);
	const suffix = qs.toString() ? `?${qs}` : "";
	const res = await req(cfg, `${API}/notifications${suffix}`, init);
	const data = await res.json();
	return {
		notifications: Array.isArray(data?.notifications) ? data.notifications : [],
		nextCursor: typeof data?.nextCursor === "string" && data.nextCursor ? data.nextCursor : undefined,
		unreadCount: typeof data?.unreadCount === "number" ? data.unreadCount : 0,
	};
}

// With ids, only those rows are marked, so unread rows on pages the screen has
// not loaded yet stay unread. Without ids, every unread row on the machine is.
export async function markAllNotificationsRead(cfg: ServerConfig, ids?: string[]): Promise<void> {
	await req(cfg, `${API}/notifications/read-all`, {
		method: "POST",
		...(ids && ids.length > 0 ? { body: JSON.stringify({ ids }) } : {}),
	});
}

