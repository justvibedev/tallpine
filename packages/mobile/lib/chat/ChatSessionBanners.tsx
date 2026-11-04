import { Feather } from "../icons";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import { haptics } from "../haptics";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { controllerStoppedBanner, mcpBanner, reauthBanner, threadBanner, type BannerCopy } from "./conversationBanners";
import { brokenMcpServers } from "./types";
import { useMobileConversation } from "./useConversation";
import { signInCommand } from "./ChatSessionScreen";
import { makeStyles } from "./chatSessionStyles";

export function ConversationBanners({ snapshot, startFailure, starting, brokenServers, resuming, resumeError, terminated, mcpReloading, mcpError, mcpReloadSupported, turnInFlight, onResume, onReload, onOpenShell, dismissed, onDismiss }: { snapshot: NonNullable<ReturnType<typeof useMobileConversation>["snapshot"]>; startFailure?: string; starting: boolean; brokenServers: ReturnType<typeof brokenMcpServers>; resuming: boolean; resumeError?: string; terminated: boolean; mcpReloading: boolean; mcpError?: string; mcpReloadSupported: boolean; turnInFlight: boolean; onResume(): void; onReload(): void; onOpenShell(): void; dismissed: ReadonlySet<string>; onDismiss(key: string): void }) {
	const thread = snapshot.threadState;
	const reauthAt = snapshot.account?.reauthRequiredAt;
	return <>
		{reauthAt ? <DismissibleBanner copy={reauthBanner(reauthAt, signInCommand(snapshot.harness))} dismissed={dismissed} onDismiss={onDismiss} tone="danger" icon="key" action="Open shell" onPress={onOpenShell} /> : null}
		{startFailure ? <DismissibleBanner copy={{ key: `start:${startFailure}`, title: "Session failed to start", body: startFailure }} dismissed={dismissed} onDismiss={onDismiss} tone="danger" icon="alert-triangle" action={resuming ? "Retrying…" : "Retry"} secondary="Shell" onPress={resuming ? undefined : onResume} onSecondary={onOpenShell} /> : !starting && snapshot.controller.state === "stopped" ? <DismissibleBanner copy={controllerStoppedBanner(terminated, resumeError ?? snapshot.controller.error)} dismissed={dismissed} onDismiss={onDismiss} tone="danger" icon="power" action={terminated ? (resuming ? "Restoring…" : "Restore") : (resuming ? "Resuming…" : "Resume")} secondary="Shell" onPress={resuming ? undefined : onResume} onSecondary={onOpenShell} /> : null}
		{/* Passing states clear themselves, so there is nothing to close. */}
		{!startFailure && !starting && (snapshot.controller.state === "recovering" || snapshot.controller.state === "connecting") ? <InlineBanner tone="warning" icon="loader" title={snapshot.controller.state === "recovering" ? "Reconnecting to the agent…" : "Starting the agent…"} /> : null}
		{threadBanner(thread?.status) ? <DismissibleBanner copy={threadBanner(thread?.status)!} dismissed={dismissed} onDismiss={onDismiss} tone={thread?.status === "system_error" ? "danger" : "warning"} icon="alert-triangle" /> : null}
		{brokenServers.length ? <DismissibleBanner copy={mcpBanner(brokenServers, mcpError)!} dismissed={dismissed} onDismiss={onDismiss} tone="warning" icon="tool" action={mcpReloadSupported && !turnInFlight ? (mcpReloading ? "Reloading…" : "Reload") : undefined} onPress={mcpReloading ? undefined : onReload} /> : null}
	</>;
}

export type InlineBannerProps = { tone: "warning" | "danger" | "muted"; icon: keyof typeof Feather.glyphMap; title: string; body?: string; action?: string; secondary?: string; onPress?(): void; onSecondary?(): void; onDismiss?(): void };

/** A banner the reader can close; it stays closed until what it reports changes. */
export function DismissibleBanner({ copy, dismissed, onDismiss, ...props }: Omit<InlineBannerProps, "title" | "body" | "onDismiss"> & { copy: BannerCopy | undefined; dismissed: ReadonlySet<string>; onDismiss(key: string): void }) {
	if (!copy || dismissed.has(copy.key)) return null;
	return <InlineBanner {...props} title={copy.title} body={copy.body} onDismiss={() => onDismiss(copy.key)} />;
}

// Headline plus at most two lines of detail, like the desktop's status banners.
export function InlineBanner({ tone, icon, title, body, action, secondary, onPress, onSecondary, onDismiss }: InlineBannerProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const color = tone === "danger" ? t.red : tone === "warning" ? t.amber : t.textTertiary;
	const fill = tone === "danger" ? t.tintRed : tone === "warning" ? t.tintAmber : t.bgSubtle;
	return (
		<View style={[styles.banner, { backgroundColor: fill }]}>
			<Feather name={icon} size={12} color={color} style={styles.bannerIcon} />
			<View style={styles.bannerCopy}>
				<Text style={[styles.bannerTitle, { color: tone === "muted" ? t.textSecondary : color }]} numberOfLines={1}>{title}</Text>
				{body ? <Text style={styles.bannerText} numberOfLines={2}>{body}</Text> : null}
			</View>
			{secondary ? <Pressable hitSlop={7} onPress={() => { haptics.tap(); onSecondary?.(); }}><Text style={styles.bannerSecondary}>{secondary}</Text></Pressable> : null}
			{action ? <Pressable hitSlop={7} onPress={() => { haptics.tap(); onPress?.(); }}><Text style={[styles.bannerAction, { color }]}>{action}</Text></Pressable> : null}
			{onDismiss ? <Pressable accessibilityRole="button" accessibilityLabel={`Close: ${title}`} hitSlop={10} onPress={() => { haptics.tap(); onDismiss(); }} style={styles.bannerClose}><Feather name="x" size={15} color={t.textTertiary} /></Pressable> : null}
		</View>
	);
}

export function Unavailable({ message, onShell, openingShell }: { message: string; onShell(): void; openingShell: boolean }) { return <Centered icon="alert-triangle" title="Conversation unavailable" message={`${message}\n\nThe worktree is untouched. You can still open a plain shell in it.`} action={openingShell ? "Opening…" : "Open worktree shell"} onAction={onShell} />; }
export function Centered({ icon, title, message, spinning, action, onAction }: { icon: keyof typeof Feather.glyphMap; title: string; message?: string; spinning?: boolean; action?: string; onAction?(): void }) { const t = useTheme(); const styles = useThemedStyles(makeStyles); return <View style={styles.center}>{spinning ? <ActivityIndicator color={t.accent} /> : <Feather name={icon} size={20} color={t.amber} />}<Text style={styles.centerTitle}>{title}</Text>{message ? <Text style={styles.centerCopy}>{message}</Text> : null}{action ? <Pressable onPress={() => { haptics.tap(); onAction?.(); }} style={styles.centerAction}><Text style={styles.centerActionText}>{action}</Text></Pressable> : null}</View>; }
