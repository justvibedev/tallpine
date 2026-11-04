/**
 * Notification signal policy for the main process.
 *
 * The renderer forwards every `notification_created` event to `notifications:show`,
 * so the type reaching main is always one defined by
 * `backend/internal/domain/notification.go`. These helpers keep the decision of
 * "toast vs. active attention signal" in one typed, testable place rather than as
 * hardcoded string literals scattered through the IPC handler.
 */

/**
 * How insistently a notification signals, as set by the backend
 * (`domain.NotificationType.Urgency`, carried on the wire as `urgency`). Main
 * never lists notification types itself: a new type or a changed rule in
 * `backend/internal/domain/notification.go` reaches here without a copy to update.
 */
export type NotificationUrgency = "critical" | "attention" | "info";

/**
 * Whether to fire the Windows/Linux taskbar flash (and the sound): any urgency
 * above "info". A merged or closed PR is worth a toast, but should not demand
 * attention as insistently as an agent blocked or a plan waiting on the user.
 * A missing or unknown urgency (an older host) stays informational.
 *
 * The macOS dock bounce is deliberately NOT gated by this: every notification
 * bounces there, with urgency carried by {@link dockBounceType}.
 */
export function shouldSignalAttention(urgency: string | undefined): boolean {
	return urgency === "critical" || urgency === "attention";
}

/**
 * Whether a new macOS dock bounce should replace the pending one. A pending
 * "critical" bounce (agent blocked on the user) is never replaced: a later
 * informational notification must not downgrade it to a one-shot bounce, or
 * the blocked-agent signal would stop lasting until the user returns.
 */
export function shouldReplaceBounce(pending: { critical: boolean } | null): boolean {
	return pending === null || !pending.critical;
}

/**
 * Whether to fire an OS toast. Deliberately independent of the type list: every
 * backend notification type gets a toast, so adding a new type in
 * `notification.go` can never silently drop its toast (the bug this replaced).
 */
export function shouldToast(notification: { title?: string }, isSupported: boolean): boolean {
	return Boolean(notification.title) && isSupported;
}

/**
 * Platforms where Electron's Notification honours `silent`. The macOS
 * (cocoa_notification.mm) and Windows (windows_toast_notification.cc)
 * presenters read it; the Linux libnotify presenter never does and sends no
 * freedesktop `suppress-sound` hint, so on Linux the daemon alone decides
 * whether a toast chimes (Electron 33.4.11). The "Sound notifications"
 * preference therefore promises control over Tallpine-owned audio everywhere, and
 * over the OS chime only where this returns true.
 */
export function osToastChimeControllable(platform: NodeJS.Platform): boolean {
	return platform === "darwin" || platform === "win32";
}

/**
 * The `silent` value for the OS toast, or `undefined` where the platform
 * ignores it (see osToastChimeControllable) so the call site cannot read a
 * muted toast into a value the presenter drops on the floor.
 *
 * Where it is honoured, two reasons mute: the user turned sound notifications
 * off (the preference has to silence every chime, not just ours), or Tallpine is
 * about to play its own sound and must not layer the system chime on top of
 * it. Otherwise (sound on, informational type) the toast keeps its native
 * chime, which is the only sound that type gets.
 */
export function toastSilent(
	platform: NodeJS.Platform,
	soundNotificationsEnabled: boolean,
	playsSound: boolean,
): boolean | undefined {
	if (!osToastChimeControllable(platform)) return undefined;
	return !soundNotificationsEnabled || playsSound;
}

/**
 * macOS dock bounce style. A "critical" notification (blocked on the user) keeps
 * bouncing until the app is activated; anything else bounces once
 * ("informational"). This is where urgency lives, so every notification can
 * signal without a merged PR nagging as insistently as a blocked agent.
 */
export function dockBounceType(urgency: string | undefined): "critical" | "informational" {
	return urgency === "critical" ? "critical" : "informational";
}
