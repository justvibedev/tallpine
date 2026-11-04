import { Feather } from "../icons";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import { NotificationTypeIcon } from "../notification-type-icon";
import { notificationVisual, relativeTime } from "../notificationView";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { Dot } from "../ui";
import { NotificationSwipeRow } from "../NotificationSwipeRow";
import { MachineIdentity } from "../MachineIdentity";
import { machineAppearance } from "../machineAppearance";
import { FeedItem } from "./notificationTypes";
import { makeStyles } from "./notificationStyles";

export function NotificationSectionHeader({ title, count }: { title: string; count: number }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.sectionHeader}>
			<Text style={styles.sectionLabel}>{title}</Text>
			<View style={styles.sectionRule} />
			<Text style={styles.sectionCount}>{count}</Text>
		</View>
	);
}

export function NotificationRow({ item, highlighted, now, action, restoring, onPress, onRestore, onClear }: {
	item: FeedItem;
	highlighted: boolean;
	now: number;
	action: "open" | "review" | "restore" | "plan" | "prs" | "none";
	restoring: boolean;
	onPress: () => void;
	onRestore: () => void;
	onClear: () => boolean;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const visual = notificationVisual(t, item.type, item.urgency);
	const unread = item.status === "unread" || highlighted;

	return (
		<NotificationSwipeRow onClear={onClear}>
			<View style={[styles.row, action === "none" && styles.rowInert]}>
				<Pressable
					onPress={onPress}
					accessibilityRole="button"
					accessibilityState={{ disabled: action === "none" }}
					accessibilityLabel={`${item.title || visual.label}, ${visual.label}, ${item.hostName} computer`}
					accessibilityHint={action === "restore" ? "This session is terminated. Use the restore button to bring it back." : undefined}
					accessibilityActions={[{ name: "delete", label: "Clear notification" }]}
					onAccessibilityAction={(event) => {
						if (event.nativeEvent.actionName === "delete") onClear();
					}}
					style={({ pressed }) => [styles.rowTap, pressed && action !== "none" && styles.rowPressed]}
				>
					<View style={styles.rowCopy}>
						<View style={styles.metaRow}>
							<NotificationTypeIcon icon={visual.icon} color={unread ? visual.color : t.textFaint} />
							<Text style={[styles.kind, unread && { color: visual.color }]} numberOfLines={1}>{visual.label}</Text>
							{unread ? <Dot color={t.accent} size={7} /> : null}
							<MachineIdentity
								name={item.hostName}
								appearance={machineAppearance({ id: item.hostId, name: item.hostName, appearance: item.hostAppearance })}
								style={styles.machineIdentity}
							/>
							<Text style={styles.time}>{relativeTime(item.createdAt, now)}</Text>
						</View>
						<Text style={[styles.title, unread && styles.titleUnread]} numberOfLines={1}>{item.title || visual.label}</Text>
						{item.body ? <Text style={styles.body} numberOfLines={1}>{item.body}</Text> : null}
					</View>
				</Pressable>
				{action === "restore" ? (
					<Pressable
						onPress={onRestore}
						disabled={restoring}
						accessibilityRole="button"
						accessibilityLabel={`Restore ${item.title || visual.label}`}
						accessibilityState={{ busy: restoring, disabled: restoring }}
						style={({ pressed }) => [styles.restoreButton, pressed && styles.restorePressed]}
					>
						{restoring ? <ActivityIndicator size="small" color={t.textSecondary} /> : <Feather name="rotate-ccw" size={20} color={t.textSecondary} />}
					</Pressable>
				) : null}
			</View>
		</NotificationSwipeRow>
	);
}
