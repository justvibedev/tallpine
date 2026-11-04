import { Feather } from "../lib/icons";
import { CameraView, useCameraPermissions } from "expo-camera";
import * as ExpoLinking from "expo-linking";
import { useFocusEffect, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Alert, AppState, Linking, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { pickNormalLens } from "../lib/cameraLens";
import { LOCAL_NETWORK_HINT, type ConnectionErrorCopy } from "../lib/connectionError";
import { haptics } from "../lib/haptics";
import { clearOnboardingSkipped } from "../lib/onboardingStore";
import { DESKTOP_PAIRING_PATH, PRODUCT_NAME } from "../lib/brand";
import { describePairingOffer, isPairingLink, parsePairingCode } from "../lib/pairingCode";
import { findHost, type Host } from "../lib/hosts";
import { createScanGate } from "../lib/scanGate";
import { connectSheetRoute } from "../lib/sheetResult";
import { useApp } from "../lib/store";
import { Button, NumberedStep } from "../lib/ui";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { MinimalBackButton } from "../lib/MinimalBackButton";
import { iconSize, space } from "../lib/tokens";
import { backOr } from "../lib/backNavigation";
import { PairProgressPanel, type PairPanelAction } from "../lib/PairProgressPanel";
import { markStep, noteProgress, startPairProgress, type PairProgress, type PairStepId, type PairStepState } from "../lib/pairProgress";
import { withTimeout } from "../lib/pairLeave";
import type { ServerConfig } from "../lib/config";
import { RECONNECT_WAIT_MS } from "../lib/pair/pairConstants";
import { usePairFlow } from "../lib/pair/usePairFlow";
import { CameraGate, Corner, NoticeAction } from "../lib/pair/PairParts";
import { makeStyles } from "../lib/pair/pairStyles";

// Pairing links already acted on in this process (module scope survives remounts).
const consumedPairingLinks = new Set<string>();

export default function PairScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const navigation = useNavigation();
	const insets = useSafeAreaInsets();
	const { from, repairHostId } = useLocalSearchParams<{ from?: string; repairHostId?: string }>();
	const fromOnboarding = from === "onboarding";
	const { reloadConfig } = useApp();
	const [permission, requestPermission] = useCameraPermissions();
	const [failure, setFailure] = useState<ConnectionErrorCopy | null>(null);
	const [busy, setBusy] = useState(false);
	// The steps of the current attempt, kept after it ends so a failure still
	// shows where it stopped and can be copied.
	const [progress, setProgress] = useState<PairProgress | null>(null);
	// Paired, but the first load of sessions failed twice: offer a retry and a
	// way on instead of leaving a spinner up.
	const [stalled, setStalled] = useState(false);
	const pairedConfig = useRef<ServerConfig | null>(null);
	const step = useCallback((id: PairStepId, state: PairStepState, note?: string) => {
		setProgress((p) => markStep(p ?? startPairProgress(Date.now()), id, state, Date.now(), note));
	}, []);
	const note = useCallback((text: string) => {
		setProgress((p) => noteProgress(p ?? startPairProgress(Date.now()), text, Date.now()));
	}, []);
	// Set while a scan keeps trying a LAN address that iOS may be holding behind
	// its Local Network prompt; shown as progress rather than as an error.
	const [awaitingLocalNetwork, setAwaitingLocalNetwork] = useState(false);
	const [gate] = useState(createScanGate);
	// Answering that prompt sends the app inactive and straight back to active —
	// the only sign iOS gives that the user has decided. It is approximate: Control
	// Center, a notification or Face ID cycle the same way, which at worst ends a
	// wait early or retries a failed code once.
	const promptAnsweredAt = useRef<number | null>(null);
	// Read from the AppState listener, which outlives any one render.
	const latestPair = useRef<(code: string) => void>(() => {});
	useEffect(() => {
		let wasInactive = false;
		const sub = AppState.addEventListener("change", (state) => {
			if (state === "inactive") wasInactive = true;
			else if (state === "active" && wasInactive) {
				wasInactive = false;
				promptAnsweredAt.current = Date.now();
				// Answered after the wait gave up: connect now rather than
				// leaving the error up until the user finds Try again. Only while
				// this screen is in front — the manual-connect and name sheets sit
				// on top of it, and a pair behind them could push a second sheet or
				// close the screen mid-typing. Checked before promptAnswered() so
				// the one retry is not spent while hidden.
				if (!focused.current) return;
				const code = gate.promptAnswered();
				if (code) latestPair.current(code);
			}
		});
		return () => sub.remove();
	}, [gate]);
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; };
	}, []);
	const [repairHost, setRepairHost] = useState<Host | null | undefined>(() => repairHostId ? undefined : null);
	useEffect(() => {
		if (!repairHostId) {
			setRepairHost(null);
			return;
		}
		let current = true;
		// An unreadable host list must not leave the screen on a spinner; it reads
		// as "no longer saved", which offers the way back.
		void findHost(repairHostId).catch(() => null).then((host) => {
			if (current) setRepairHost(host);
		});
		return () => { current = false; };
	}, [repairHostId]);
	// The camera keeps running under the manual-connect sheet, so a QR code in
	// frame would otherwise pair behind it. This replaces the old `manualOpen`
	// flag now that the sheet is a route pushed on top rather than local state.
	const focused = useRef(true);
	useFocusEffect(
		useCallback(() => {
			focused.current = true;
			return () => {
				focused.current = false;
			};
		}, []),
	);
	const rejected = useRef<string | null>(null);
	const pendingCode = useRef<string | null>(null);

	const camera = useRef<CameraView>(null);
	const [lens, setLens] = useState<string | undefined>(undefined);

	async function onCameraReady() {
		if (Platform.OS !== "ios" || lens) return;
		try {
			const lenses = (await camera.current?.getAvailableLensesAsync()) ?? [];
			if (typeof __DEV__ !== "undefined" && __DEV__) console.log("[pair] available lenses", lenses);
			setLens(pickNormalLens(lenses));
		} catch {
			/* keep the native default */
		}
	}

	// Reconnects the store with the new credentials. Bounded: the pairing is
	// already saved, and a slow race must not hold the user on this screen.
	async function reconnect() {
		await clearOnboardingSkipped();
		await withTimeout(reloadConfig().then(() => undefined, () => undefined), RECONNECT_WAIT_MS, undefined);
	}
	const { leave, finish, completePairing, onScan, retry } = usePairFlow({ focused, mounted, fromOnboarding, navigation, router, reconnect, pairedConfig, setStalled, setBusy, step, note, busy, rejected, setFailure, gate, setAwaitingLocalNetwork, pendingCode, setProgress, repairHost, promptAnsweredAt, latestPair });

	// A tallpine-mobile://pair#<code> link carries the whole offer: pair from it
	// directly instead of leaving the user at the scanner. Handled once per link.
	const linkedUrl = ExpoLinking.useURL();
	useEffect(() => {
		// useURL replays the cold-launch URL on every mount, so remember consumed links per process.
		if (!isPairingLink(linkedUrl) || consumedPairingLinks.has(linkedUrl)) return;
		consumedPairingLinks.add(linkedUrl);
		// Any web page or app can open the scheme, so a link only starts a pairing once the user agrees.
		const offer = parsePairingCode(linkedUrl);
		if (!offer) return;
		const { title, message } = describePairingOffer(offer);
		Alert.alert(title, message, [
			{ text: "Cancel", style: "cancel" },
			{ text: "Pair", onPress: () => void onScan({ data: linkedUrl }) },
		]);
	});

	const back = () => backOr(router);
	const stalledActions: PairPanelAction[] = [
		{ icon: "refresh-cw", title: "Try again", onPress: () => { if (pairedConfig.current) void completePairing(pairedConfig.current); } },
		{ icon: "chevron-right", title: "Continue", onPress: () => void leave() },
	];
	if (repairHost === undefined) {
		return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	}
	if (repairHostId && !repairHost) {
		return (
			<View style={[styles.screen, styles.missing, { paddingTop: insets.top + space.xl }]}>
				<Feather name="monitor-off" size={iconSize.xl} color={t.textTertiary} />
				<Text style={styles.missingTitle}>Computer no longer saved</Text>
				<Text style={styles.missingText}>Return to Settings and choose another computer.</Text>
				<Button title="Back to Settings" variant="ghost" onPress={back} />
			</View>
		);
	}

	return (
		<View style={[styles.screen, { paddingTop: insets.top }]}>
			<View style={styles.topBar}><MinimalBackButton onPress={back} /></View>

			{repairHost ? (
				<View style={styles.repairIntro}>
					<Text style={styles.repairTitle}>Reconnect {repairHost.name}</Text>
					<Text style={styles.repairText}>{`Scan the new code from ${DESKTOP_PAIRING_PATH} on that computer.`}</Text>
				</View>
			) : <View style={styles.steps}>
					<NumberedStep n={1} title={`Open ${PRODUCT_NAME} on your computer`} compact />
					<NumberedStep n={2} title={`Turn on mobile access in ${DESKTOP_PAIRING_PATH}`} compact />
					<NumberedStep n={3} title="Scan the QR code, or enter the address and pairing code manually" compact />
			</View>}

			<View style={styles.viewfinder}>
				{permission?.granted ? (
					<>
						<CameraView
							ref={camera}
							style={StyleSheet.absoluteFill}
							facing="back"
							selectedLens={lens}
							onCameraReady={onCameraReady}
							barcodeScannerSettings={{ barcodeTypes: ["qr"] }}
							// Stopped from the moment a code is taken until the attempt ends, so
							// the code still in frame cannot start a second pairing.
							active={!busy && !stalled}
							onBarcodeScanned={busy || stalled ? undefined : onScan}
						/>
						<Corner style={styles.cTL} />
						<Corner style={styles.cTR} />
						<Corner style={styles.cBL} />
						<Corner style={styles.cBR} />
					</>
				) : (
					<CameraGate
						// `permission` is null only while the hook resolves on first render.
						loading={!permission}
						// Once permanently denied, requestPermission is a silent no-op —
						// only the system settings page can flip it back.
						canAskAgain={permission?.canAskAgain ?? true}
						onRequest={requestPermission}
					/>
				)}
			</View>

			{/* From the moment a scan starts: the first race alone can take ~20 s
			    when a code's tunnel is down, and a frozen-looking scanner after the
			    haptic reads as broken. The Local Network line only while waiting on it. */}
			{progress ? (
				<PairProgressPanel
					progress={progress}
					hint={awaitingLocalNetwork && !failure ? "If asked, allow access to your local network." : undefined}
					actions={stalled ? stalledActions : []}
				/>
			) : busy || awaitingLocalNetwork ? (
				<View style={styles.notice} accessibilityLiveRegion="polite">
					<View style={styles.noticeLead}><ActivityIndicator size="small" color={t.textSecondary} /></View>
					<View style={styles.noticeBody}>
						<Text style={styles.noticeTitle}>Connecting…</Text>
						{awaitingLocalNetwork ? (
							<Text style={styles.noticeText}>If asked, allow access to your local network.</Text>
						) : null}
					</View>
				</View>
			) : null}

			{failure ? (
				<View style={styles.notice} accessibilityLiveRegion="polite">
					<View style={styles.noticeLead}><Feather name="alert-circle" size={iconSize.sm} color={t.red} /></View>
					<View style={styles.noticeBody}>
						<Text style={styles.noticeTitle}>{failure.title}</Text>
						<Text style={styles.noticeText}>{failure.message}</Text>
						{failure.showLocalNetworkHint ? <Text style={styles.noticeText}>{LOCAL_NETWORK_HINT}</Text> : null}
						<View style={styles.noticeActions}>
							{/* Re-runs the scanned code without pointing the camera at it again. */}
							<NoticeAction icon="refresh-cw" title="Try again" onPress={retry} />
							{failure.showLocalNetworkHint ? (
								<NoticeAction icon="settings" title="Open settings" onPress={() => void Linking.openSettings()} />
							) : null}
						</View>
					</View>
				</View>
			) : null}

			{/* Always reachable — including when the camera is permanently denied,
			    which would otherwise leave the user with no way forward at all. */}
			<Pressable
				onPress={() => { haptics.tap(); router.push(connectSheetRoute(() => void finish(), repairHost?.id)); }}
				style={[styles.manual, { paddingBottom: insets.bottom + 14 }]}
				accessibilityRole="button"
			>
				<Feather name="edit-3" size={iconSize.sm} color={t.textSecondary} />
				<Text style={styles.manualText}>Enter details manually</Text>
			</Pressable>
		</View>
	);
}

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";
