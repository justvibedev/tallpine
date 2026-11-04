import { ActivityIndicator, StyleSheet, Text, View } from "react-native";
import { EnvironmentPicker } from "./environment-picker";
import { MachineAvatar } from "./MachineIdentity";
import type { HostSnapshot } from "./otherHosts";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { space, type } from "./tokens";
import { Button } from "./ui";
import type { WorkerEnvironment } from "./worker-controls";
import { PRODUCT_NAME } from "./brand";
import { HTTPS_REQUIRED } from "./authCodes";
import { HTTPS_REQUIRED_MESSAGE, HTTPS_REQUIRED_TITLE } from "./secureTransport";
import { PAIRING_REMOVED_MESSAGE, PAIRING_REMOVED_TITLE } from "./revokedPairing";

export function WorkerMachineState({ host, environments, onSelectEnvironment, onRetry, onScan }: {
	host: Pick<HostSnapshot, "hostId" | "name" | "appearance" | "connection" | "errorStatus" | "errorCode">;
	environments: readonly WorkerEnvironment[];
	onSelectEnvironment: (environmentId: string) => void;
	onRetry: () => void;
	onScan: () => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const connecting = host.connection === "connecting";
	// 401: the machine dropped this pairing. HTTPS_REQUIRED: the pairing names
	// only plain addresses, which the phone never uses. Retrying cannot fix
	// either, so the only action offered is the scan that does.
	const final = connecting ? null
		: host.errorStatus === 401 ? { title: PAIRING_REMOVED_TITLE, message: PAIRING_REMOVED_MESSAGE }
			: host.errorCode === HTTPS_REQUIRED ? { title: HTTPS_REQUIRED_TITLE, message: HTTPS_REQUIRED_MESSAGE }
				: null;
	if (final) {
		return (
			<View style={styles.container}>
				<MachineAvatar appearance={host.appearance} size={space.huge * 2} />
				<Text style={styles.name}>{host.name}</Text>
				<View testID="worker-machine-status" accessibilityLabel={`${final.title} for ${host.name}`} accessibilityLiveRegion="polite" style={styles.status}>
					<Text style={styles.statusText}>{final.title}</Text>
				</View>
				<Text testID="worker-machine-hint" style={styles.hint}>{final.message}</Text>
				<View style={styles.actions}>
					<Button title="Scan" icon="maximize" onPress={onScan} />
					<EnvironmentPicker environments={environments} selectedEnvironmentId={host.hostId} onSelectEnvironment={onSelectEnvironment} />
				</View>
			</View>
		);
	}
	return (
		<View style={styles.container}>
			<MachineAvatar appearance={host.appearance} size={space.huge * 2} />
			<Text style={styles.name}>{host.name}</Text>
			<View testID="worker-machine-status" accessibilityLabel={connecting ? `Connecting to ${host.name}` : `Can't reach ${host.name}`} accessibilityLiveRegion="polite" accessibilityState={{ busy: connecting }} style={styles.status}>
				{connecting ? <ActivityIndicator size="small" color={t.textSecondary} /> : null}
				<Text style={styles.statusText}>{connecting ? "Connecting…" : "Can't reach this computer"}</Text>
			</View>
			<Text testID="worker-machine-hint" style={styles.hint}>
				{connecting ? "Workers will appear once this computer connects."
					: host.errorStatus === 426 ? `Update ${PRODUCT_NAME} on this phone and the computer to load its workers.`
						: `Check that ${PRODUCT_NAME} is running and this computer is reachable to load its workers.`}
			</Text>
			<View style={styles.actions}>
				{!connecting ? <Button title="Retry" icon="refresh-cw" variant="ghost" onPress={onRetry} /> : null}
				<EnvironmentPicker environments={environments} selectedEnvironmentId={host.hostId} onSelectEnvironment={onSelectEnvironment} />
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	container: { flex: 1, minHeight: 320, alignItems: "center", justifyContent: "center", padding: space.xxxl },
	name: { ...type.title3, color: t.textPrimary, textAlign: "center", marginTop: space.sm },
	status: { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.sm, marginTop: space.md },
	statusText: { ...type.callout, color: t.textSecondary },
	hint: { ...type.subheadline, color: t.textTertiary, textAlign: "center", marginTop: space.sm },
	actions: { alignItems: "center", gap: space.sm, marginTop: space.xxl },
});
