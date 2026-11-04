import { Feather } from "./icons";
import { PRODUCT_NAME } from "./brand";
import { useState } from "react";
import { Alert, Linking, Platform, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { ApiError, pingServer } from "./api";
import { DEFAULT_CONFIG, saveConfig, type ServerConfig } from "./config";
import { saveHost, setActiveHost, renameHost, type Host } from "./hosts";
import { adoptManualConnection, DifferentMachineError, ExchangeRefusedError, persistManualHostEdit, redeemManualCredential } from "./manualConnect";
import { exchangeForDeviceToken } from "./pairExchange";
import { runtimeExchangeDeps } from "./pairExchangeRuntime";
import { configForEndpoint } from "./connect";
import { normalizeServerHost } from "./endpoints";
import { probeEndpoint, probeIdentity } from "./connectRuntime";
import { IncompatibleHostVersionError } from "./race";
import {
	classifyConnectionFailure,
	describeConnectionFailure,
	isLocalNetworkHost,
	LOCAL_NETWORK_HINT,
	type ConnectionErrorCopy,
} from "./connectionError";
import { setLocalNetworkConfirmed } from "./localNetworkStore";
import type { Theme } from "./theme";
import { haptics } from "./haptics";
import { Button, SHEET_SCROLL_CONTENT, SheetHeader, SheetScreen } from "./ui";
import { establishManualTls, ManualTlsDeclinedError } from "./manualTls";
import { probeTls } from "./pinning";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { textSelectionColors } from "./textSelection";
import { iconSize, space, touchTarget, type } from "./tokens";

// The typing fallback behind the QR scanner, also reused to edit a pairing.
// The legacy mux port is unused against the Go daemon, so it stays hidden.
export function ManualConnectSheet({ onConnected, editingHost, editingEndpointIndex = 0 }: {
	onConnected: () => void;
	editingHost?: Host;
	editingEndpointIndex?: number;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// New pairings start blank; editing preloads only the pairing the user selected.
	const [cfg, setCfg] = useState<ServerConfig>(() => {
		const endpoint = editingHost?.endpoints[editingEndpointIndex];
		return endpoint ? configForEndpoint(endpoint, editingHost.token, editingHost.id) : { ...DEFAULT_CONFIG, password: editingHost?.token ?? "" };
	});
	const [machineName, setMachineName] = useState(editingHost?.name ?? "");
	const [busy, setBusy] = useState(false);
	const [failure, setFailure] = useState<ConnectionErrorCopy | null>(null);
	const [showPassword, setShowPassword] = useState(false);

	const set = (k: keyof ServerConfig) => (v: string) => setCfg((prev) => ({ ...prev, [k]: v }));

	async function connect() {
		setBusy(true);
		setFailure(null);
		let target = { ...cfg, host: normalizeServerHost(cfg.host), httpPort: cfg.httpPort.trim() };
		try {
			const editedEndpoint = editingHost?.endpoints[editingEndpointIndex];
			target = await establishManualTls(target, {
				probe: probeTls,
				confirm: (fingerprint) =>
					new Promise<boolean>((resolve) =>
						Alert.alert(
							"Check this security code",
							`${fingerprint}\n\nIt must match the security code shown in ${PRODUCT_NAME} → Settings → Connections on that computer. If it differs, someone else on this network may be answering.`,
							[
								{ text: "Cancel", style: "cancel", onPress: () => resolve(false) },
								{ text: "It matches", onPress: () => resolve(true) },
							],
							{ cancelable: true, onDismiss: () => resolve(false) },
						),
					),
			}, editedEndpoint);
			if (editingHost) {
				await persistManualHostEdit(editingHost, target, machineName, editingEndpointIndex, {
					probe: (endpoint) => probeEndpoint(endpoint, new AbortController().signal),
					ping: pingServer,
					saveHost,
					renameHost,
				});
				if (isLocalNetworkHost(target.host)) void setLocalNetworkConfirmed();
				haptics.success();
				onConnected();
				return;
			}
			// Identity is public; reject unsupported hosts before presenting a password.
			let hostId = "";
			try { hostId = await probeIdentity(target); }
			catch (error) { if (error instanceof IncompatibleHostVersionError) throw error; }
			// Verify BEFORE persisting. pingServer takes the config it is handed, so
			// nothing needs to be saved to test it — and saving first would leave
			// known-bad credentials on disk. The background poller retries every 8s,
			// and the daemon locks a device out for a minute after 5 failed auths,
			// so a persisted wrong password locks the user out on its own in ~40s,
			// with no further input from them.
			// The desktop shows a single-use pairing code; trade it for this
			// phone's own token before using it as a credential.
			target = await redeemManualCredential(target, (endpoint, code) => exchangeForDeviceToken(endpoint, { code }, runtimeExchangeDeps()));
			await pingServer(target);
			// Reaching a LAN address proves iOS granted Local Network access, so a
			// later scan that reaches nothing can say so at once (localNetworkWait).
			if (isLocalNetworkHost(target.host)) void setLocalNetworkConfirmed();
			await saveConfig(target);
			// Store it as a machine and select it. Writing only the legacy config
			// left resolution reconnecting the previously active machine on the
			// next launch, so a manual connection silently did not stick.
			await adoptManualConnection(target, {
				identity: async () => hostId,
				saveHost,
				setActiveHost,
			}, machineName);
			haptics.success();
			onConnected();
		} catch (e) {
			haptics.warning();
			if (e instanceof ManualTlsDeclinedError) {
				setFailure({ title: "Not connected", message: e.message, icon: "alert-circle", showLocalNetworkHint: false });
				return;
			}
			if (e instanceof DifferentMachineError) {
				setFailure({
					title: "Different computer",
					message: e.message,
					icon: "alert-circle",
					showLocalNetworkHint: false,
				});
				return;
			}
			const code = e instanceof ApiError ? e.code : undefined;
			const status = e instanceof IncompatibleHostVersionError ? 426 : e instanceof ApiError || e instanceof ExchangeRefusedError ? e.status : undefined;
			const copy = describeConnectionFailure(classifyConnectionFailure(status, code), {
				host: target.host,
				port: target.httpPort,
				platform: Platform.OS,
				name: machineName.trim() || undefined,
			});
			const auth = classifyConnectionFailure(status, code) === "auth";
			setFailure(editingHost && auth
				? { ...copy, message: "That access key was rejected. Check it and try again." }
				: auth
					? { ...copy, message: "That pairing code was refused. Each code works once and expires after 10 minutes: show a new one on that computer and enter it now." }
					: copy);
		} finally {
			setBusy(false);
		}
	}

	const validPort = /^\d+$/.test(cfg.httpPort) && Number(cfg.httpPort) >= 1 && Number(cfg.httpPort) <= 65535;
	const title = editingHost ? "Edit connection" : "Connect manually";
	const subtitle = editingHost
		? "Update this computer's name, address, port, or access key."
		: "Name this computer, then enter its address and the pairing code shown in Tallpine → Settings → Connections on that computer.";
	const form = (
		<>
			<Field
				label="NAME"
				value={machineName}
				onChangeText={setMachineName}
				placeholder="AzureLinux"
				maxLength={48}
				returnKeyType="next"
			/>
			<Field
				label="ADDRESS"
				value={cfg.host}
				onChangeText={set("host")}
				placeholder="192.168.x.x  or  my-pc.tailXXXX.ts.net"
				autoCapitalize="none"
				autoCorrect={false}
				keyboardType="url"
			/>
			<Field label="PORT" value={cfg.httpPort} onChangeText={set("httpPort")} keyboardType="number-pad" />
			<View style={styles.field}>
				<Text style={styles.fieldLabel}>{editingHost ? "ACCESS KEY" : "PAIRING CODE"}</Text>
				<View style={styles.passwordRow}>
					<TextInput
						value={cfg.password}
						onChangeText={set("password")}
						placeholder={editingHost ? "Access key" : "Pairing code"}
						placeholderTextColor={t.textFaint}
						{...textSelectionColors(t)}
						autoCapitalize="none"
						autoCorrect={false}
						secureTextEntry={!showPassword}
						style={[styles.input, styles.passwordInput]}
					/>
					<Pressable
						accessibilityRole="button"
						accessibilityLabel={showPassword ? "Hide code" : "Show code"}
						onPress={() => {
							haptics.tap();
							setShowPassword((visible) => !visible);
						}}
						style={({ pressed }) => [styles.passwordToggle, pressed && styles.passwordTogglePressed]}
					>
						<Feather name={showPassword ? "eye-off" : "eye"} size={iconSize.lg} color={t.textSecondary} />
					</Pressable>
				</View>
			</View>

			{failure ? (
				<View style={styles.errorBox}>
					<Feather name="alert-circle" size={iconSize.sm} color={t.red} />
					<View style={{ flex: 1 }}>
						<Text style={styles.errorText}>{failure.message}</Text>
						{failure.showLocalNetworkHint ? (
							<>
								<Text style={[styles.errorText, { marginTop: space.xs }]}>{LOCAL_NETWORK_HINT}</Text>
								<Button
									title="Open settings"
									variant="ghost"
									icon="settings"
									onPress={() => Linking.openSettings()}
									style={{ marginTop: space.sm }}
								/>
							</>
						) : null}
					</View>
				</View>
			) : null}

			<Button
				title={editingHost ? "Save changes" : "Connect"}
				icon={editingHost ? "check" : "link"}
				loading={busy}
				disabled={!machineName.trim() || !cfg.host.trim() || !validPort}
				onPress={connect}
				style={{ marginTop: space.lg }}
			/>
		</>
	);

	// Android's sheet is sized by detents rather than by its content (see
	// CONNECT_SHEET_OPTIONS — two detents are what lets the OS expand it for the
	// keyboard). A detent gives the content a definite height, so the form scrolls
	// within it and the Connect button stays reachable once the sheet expands.
	if (Platform.OS !== "ios") {
		return (
			<ScrollView
				style={styles.screen}
				contentContainerStyle={SHEET_SCROLL_CONTENT}
				keyboardShouldPersistTaps="handled"
			>
				<SheetHeader
					title={title}
					subtitle={subtitle}
				/>
				{form}
			</ScrollView>
		);
	}

	// iOS lifts a presented form sheet over the keyboard by itself.
	return (
		<SheetScreen title={title} subtitle={subtitle}>
			{form}
		</SheetScreen>
	);
}

function Field({ label, ...input }: { label: string } & React.ComponentProps<typeof TextInput>) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.field}>
			<Text style={styles.fieldLabel}>{label}</Text>
			<TextInput {...input} style={styles.input} placeholderTextColor={t.textFaint} {...textSelectionColors(t)} />
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgSurface },
		field: { marginTop: space.lg },
		fieldLabel: { fontFamily: "Geist_600SemiBold",
			color: t.textTertiary,
			fontSize: type.caption2.fontSize,
			fontWeight: "600",
			letterSpacing: 1.1,
			marginBottom: space.xs,
		},
		input: { fontFamily: "Geist_400Regular",
			backgroundColor: t.bgElevated,
			borderWidth: 1,
			borderColor: t.borderDefault,
			borderRadius: 8, borderCurve: "continuous",
			paddingHorizontal: space.md,
			paddingVertical: space.md,
			color: t.textPrimary,
			fontSize: type.subheadline.fontSize,
		},
		passwordRow: {
			flexDirection: "row",
			alignItems: "center",
			backgroundColor: t.bgElevated,
			borderWidth: 1,
			borderColor: t.borderDefault,
			borderRadius: 8, borderCurve: "continuous",
		},
		passwordInput: { flex: 1, backgroundColor: "transparent", borderWidth: 0, paddingRight: 0 },
		passwordToggle: {
			width: touchTarget,
			minHeight: touchTarget,
			alignItems: "center",
			justifyContent: "center",
		},
		passwordTogglePressed: { opacity: 0.6 },
		toggleRow: {
			flexDirection: "row",
			alignItems: "center",
			justifyContent: "space-between",
			marginTop: space.lg,
		},
		toggleHint: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, marginTop: space.xs },
		toggleLabel: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.subheadline.fontSize, flex: 1 },
		errorBox: {
			flexDirection: "row",
			gap: space.sm,
			alignItems: "flex-start",
			backgroundColor: t.tintRed,
			borderRadius: 8, borderCurve: "continuous",
			padding: space.md,
			marginTop: space.lg,
		},
		errorText: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	});
