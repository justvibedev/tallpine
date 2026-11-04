import { getInstallId } from "../installId";
import type { ServerConfig } from "../config";
import { API, ApiError, DISCONNECT_REQUEST_TIMEOUT_MS, req } from "./http";

// ---- Push notifications -----------------------------------------------------

// Register (idempotent upsert) this device's Expo push token with the daemon so
// its dispatcher can deliver OS push notifications. Keyed daemon-side by install ID.
export async function registerPushDevice(
	cfg: ServerConfig,
	device: { token: string; platform?: string; deviceName?: string; hostName?: string },
): Promise<void> {
	const installId = await getInstallId();
	await req(cfg, `${API}/push/devices`, {
		method: "POST",
		body: JSON.stringify({ ...device, installId }),
	});
}

// Announce this device's identity to the daemon with no push token, so the
// desktop roster shows a paired phone the moment it connects — independent of
// notification permission. Posts to the same /push/devices route as
// registerPushDevice, just without a `token` field; the daemon upserts by
// installId, so a later registerForPush call attaches the token to this same
// row instead of creating a second one.
export async function announceDevice(
	cfg: ServerConfig,
	device: { installId: string; platform?: string; deviceName?: string },
): Promise<void> {
	await req(cfg, `${API}/push/devices`, {
		method: "POST",
		body: JSON.stringify(device),
	});
}

// Unregister this device's push token (best-effort on disconnect/unpair). The
// token's [ ] brackets must be URL-encoded for the path segment.
export async function unregisterPushDevice(cfg: ServerConfig, token: string): Promise<void> {
	await req(cfg, `${API}/push/devices/${encodeURIComponent(token)}`, { method: "DELETE" });
}

// Tell a daemon this phone is no longer paired with it, so it drops the device
// from its roster entirely. Distinct from unregisterPushDevice, which only
// detaches the push token and leaves the phone listed as notifications-off —
// correct when the user switches notifications off, wrong when they disconnect,
// which would leave the old desktop showing a phone that has moved on.
//
// Prefers the install id and falls back to the token so the call still works
// from a build that predates install ids.
export async function unpairFromDaemon(cfg: ServerConfig, id: string): Promise<void> {
	await req(cfg, `${API}/push/pairings/${encodeURIComponent(id)}`, { method: "DELETE" }, DISCONNECT_REQUEST_TIMEOUT_MS);
}

// Mark a notification read (best-effort on notification tap) so unread counts
// stay consistent with the web dashboard.
export async function markNotificationRead(cfg: ServerConfig, id: string): Promise<void> {
	await req(cfg, `${API}/notifications/${encodeURIComponent(id)}`, {
		method: "PATCH",
		body: JSON.stringify({ status: "read" }),
	});
}

export async function clearNotification(cfg: ServerConfig, id: string): Promise<void> {
	try {
		await req(cfg, `${API}/notifications/${encodeURIComponent(id)}`, { method: "DELETE" });
	} catch (cause) {
		// Another client may have cleared the same row already.
		if (!(cause instanceof ApiError) || cause.status !== 404 || cause.code !== "NOTIFICATION_NOT_FOUND") throw cause;
	}
}

// Clears the machine's whole history, not just the rows the screen has loaded.
export async function clearAllNotifications(cfg: ServerConfig): Promise<void> {
	await req(cfg, `${API}/notifications`, { method: "DELETE" });
}

