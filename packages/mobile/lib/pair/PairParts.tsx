import { Feather } from "../icons";
import { Linking, Pressable, Text, View } from "react-native";
import { haptics } from "../haptics";
import { PRODUCT_NAME } from "../brand";
import { Button } from "../ui";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { iconSize, space } from "../tokens";
import { makeStyles } from "./pairStyles";

// Quiet text action, matching "Enter details manually" below it.
export function NoticeAction({ icon, title, onPress }: { icon: "refresh-cw" | "settings"; title: string; onPress: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<Pressable
			onPress={() => { haptics.tap(); onPress(); }}
			style={({ pressed }) => [styles.noticeAction, pressed && { opacity: 0.6 }]}
			accessibilityRole="button"
			hitSlop={8}
		>
			<Feather name={icon} size={iconSize.sm} color={t.textPrimary} />
			<Text style={styles.noticeActionText}>{title}</Text>
		</Pressable>
	);
}

export function Corner({ style }: { style: object }) {
	const styles = useThemedStyles(makeStyles);
	return <View style={[styles.corner, style]} />;
}

export function CameraGate({
	loading,
	canAskAgain,
	onRequest,
}: {
	loading: boolean;
	canAskAgain: boolean;
	onRequest: () => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	if (loading) {
		return (
			<View style={styles.gate}>
				<Text style={styles.gateHint}>Starting camera…</Text>
			</View>
		);
	}
	return (
		<View style={styles.gate}>
			<Feather name="camera-off" size={iconSize.xl} color={t.textTertiary} />
			<Text style={styles.gateTitle}>Camera access needed</Text>
			<Text style={styles.gateHint}>
				{canAskAgain
					? `${PRODUCT_NAME} uses the camera only to read a pairing QR code.`
					: `Camera access is turned off for ${PRODUCT_NAME}. Enable it in system settings, or enter your details manually below.`}
			</Text>
			{canAskAgain ? (
				// App Review 5.1.1(iv): the button ahead of the system permission
				// prompt must read "Continue"/"Next", never "Allow ...", so the grant
				// decision is only ever made in the system dialog itself.
				<Button title="Continue" onPress={onRequest} style={{ marginTop: space.lg }} />
			) : (
				<Button
					title="Open settings"
					variant="ghost"
					icon="settings"
					onPress={() => Linking.openSettings()}
					style={{ marginTop: space.lg }}
				/>
			)}
		</View>
	);
}
