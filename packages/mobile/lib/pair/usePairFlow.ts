import { useRouter } from "expo-router";
import { useRef } from "react";
import { Platform } from "react-native";
import { pingServer } from "../api";
import { saveConfig } from "../config";
import { classifyConnectionFailure, describeConnectionFailure, describeUnreachableCode, isLocalNetworkHost, type ConnectionErrorCopy } from "../connectionError";
import { haptics } from "../haptics";
import { completeOnboarding } from "../onboardingNavigation";
import { pairFromCode, type MachineNameChoice } from "../pairFlow";
import { exchangeForDeviceToken } from "../pairExchange";
import { runtimeExchangeDeps } from "../pairExchangeRuntime";
import { registerForPush } from "../push";
import { dropMachineUnpairs } from "../pendingUnpair";
import { isLegacyPairingCode, parsePairingCode } from "../pairingCode";
import { findHost, saveHost, setActiveHost, type Host } from "../hosts";
import { probeEndpoint } from "../connectRuntime";
import { raceEndpoints } from "../race";
import { mayAwaitLocalNetwork, raceAwaitingLocalNetwork } from "../localNetworkWait";
import { loadLocalNetworkConfirmed, setLocalNetworkConfirmed } from "../localNetworkStore";
import { machineNameSheetRoute } from "../sheetResult";
import { backOr } from "../backNavigation";
import { startPairProgress, type PairProgress, type PairStepId, type PairStepState } from "../pairProgress";
import { waitForSheetToSettle } from "../pairLeave";
import type { ServerConfig } from "../config";
import { ScanGate } from "../scanGate";
import { LEAVE_CHECK_MS, LOAD_ATTEMPTS, LOAD_RETRY_MS, sleep } from "./pairConstants";

export function usePairFlow({
	focused,
	mounted,
	fromOnboarding,
	navigation,
	router,
	reconnect,
	pairedConfig,
	setStalled,
	setBusy,
	step,
	note,
	busy,
	rejected,
	setFailure,
	gate,
	setAwaitingLocalNetwork,
	pendingCode,
	setProgress,
	repairHost,
	promptAnsweredAt,
	latestPair,
}: {
	focused: React.RefObject<boolean>;
	mounted: React.RefObject<boolean>;
	fromOnboarding: boolean;
	navigation: Parameters<typeof completeOnboarding>[0];
	router: ReturnType<typeof useRouter>;
	reconnect: () => Promise<void>;
	pairedConfig: React.RefObject<ServerConfig | null>;
	setStalled: React.Dispatch<React.SetStateAction<boolean>>;
	setBusy: React.Dispatch<React.SetStateAction<boolean>>;
	step: (id: PairStepId, state: PairStepState, note?: string | undefined) => void;
	note: (text: string) => void;
	busy: boolean;
	rejected: React.RefObject<string | null>;
	setFailure: React.Dispatch<React.SetStateAction<ConnectionErrorCopy | null>>;
	gate: ScanGate;
	setAwaitingLocalNetwork: React.Dispatch<React.SetStateAction<boolean>>;
	pendingCode: React.RefObject<string | null>;
	setProgress: React.Dispatch<React.SetStateAction<PairProgress | null>>;
	repairHost: Host | null | undefined;
	promptAnsweredAt: React.RefObject<number | null>;
	latestPair: React.RefObject<(code: string) => void>;
}) {

	// Leaves exactly once, after any sheet above this screen has finished
	// closing. A pop issued while the name sheet was still animating away could
	// be lost natively, leaving this screen up with a frozen spinner.
	const left = useRef(false);
	async function leave() {
		if (left.current) return;
		left.current = true;
		await waitForSheetToSettle({ isFocused: () => focused.current, sleep });
		if (!mounted.current) return;
		if (fromOnboarding) completeOnboarding(navigation);
		else backOr(router);
		// Still in front after navigating means the pop went nowhere: go home.
		setTimeout(() => { if (mounted.current && focused.current) router.replace("/"); }, LEAVE_CHECK_MS);
	}

	// Manual connect: the sheet already verified and saved the computer.
	async function finish() {
		await reconnect();
		await leave();
	}

	// Paired: load the sessions once with the new pairing so the last step is
	// real, then leave. Two failed loads stop here with a retry rather than a
	// spinner that never ends.
	async function completePairing(config: ServerConfig) {
		pairedConfig.current = config;
		setStalled(false);
		setBusy(true);
		step("load", "active");
		await reconnect();
		for (let attempt = 1; attempt <= LOAD_ATTEMPTS; attempt++) {
			if (!mounted.current) return;
			try {
				const count = await pingServer(config);
				step("load", "done", `${count} ${count === 1 ? "session" : "sessions"}`);
				setBusy(false);
				await leave();
				return;
			} catch (e) {
				const status = typeof (e as { status?: unknown })?.status === "number" ? (e as { status: number }).status : undefined;
				note(`Loading sessions failed (attempt ${attempt}: ${status === undefined ? "no answer" : `the computer answered ${status}`})`);
				if (attempt < LOAD_ATTEMPTS) await sleep(LOAD_RETRY_MS);
			}
		}
		if (!mounted.current) return;
		step("load", "failed");
		setBusy(false);
		setStalled(true);
	}

	async function onScan({ data }: { data: string }) {
		if (busy || !focused.current) return;
		// Cheap reject first: the camera sees every barcode in frame, and only a
		// code we can actually parse should stop the scanner.
		const offer = parsePairingCode(data);
		if (!offer) {
			if (rejected.current !== data) {
				rejected.current = data;
				// A v1 code is a recognisable thing, not noise: say what to do
				// about it rather than claiming it is not a pairing code.
				const reason = isLegacyPairingCode(data) ? "outdated-desktop" : "not-tallpine-qr";
				setFailure(describeConnectionFailure(reason, { host: "", port: "", platform: Platform.OS }));
			}
			return;
		}
		rejected.current = null;
		if (!gate.sighting(data, Date.now())) return;
		// Felt the moment the code is read, so nobody has to watch the screen to
		// know the scan took; success or warning follows when the attempt ends.
		haptics.tap();
		await pair(data, offer);
	}

	// Races the code's endpoints, verifies the winner, then stores the machine.
	// The scanned code is kept so "Try again" can re-run the whole thing rather
	// than making the user re-scan.
	async function pair(code: string, offer?: ReturnType<typeof parsePairingCode>) {
		if (!gate.start()) return;
		try {
			await attemptPair(code, offer);
		} catch (e) {
			// An attempt that threw (storage, navigation) never reported how it
			// ended. Without this the scanner would stay claimed and ignore scans,
			// and the spinner would just stop with nothing to act on.
			console.warn("[pair] attempt failed", e);
			setAwaitingLocalNetwork(false);
			setBusy(false);
			if (gate.release()) {
				// Recorded as a failure so a code still in view waits to be pointed
				// at again instead of retrying every frame.
				gate.failed(code, Date.now());
				haptics.warning();
				setFailure({
					title: "Couldn't pair",
					icon: "alert-circle",
					message: "Something went wrong on this phone. Try again.",
					showLocalNetworkHint: false,
				});
			}
		}
	}

	async function attemptPair(code: string, offer?: ReturnType<typeof parsePairingCode>) {
		pendingCode.current = code;
		setBusy(true);
		setFailure(null);
		setStalled(false);
		setProgress(startPairProgress(Date.now()));

		// A generic scan of a machine already in the list is also an in-place
		// refresh. Preserve its human-readable name instead of asking again.
		const knownHost = repairHost ?? (offer?.hostId ? await findHost(offer.hostId) : null);
		const localNetworkConfirmed = await loadLocalNetworkConfirmed();
		promptAnsweredAt.current = null;
		let awaitedLocalNetwork = false;
		const result = await pairFromCode(code, {
			race: (endpoints, expectedHostId) => {
				const once = () => raceEndpoints(endpoints, expectedHostId, probeEndpoint);
				if (!mayAwaitLocalNetwork(endpoints, Platform.OS, localNetworkConfirmed)) return once();
				awaitedLocalNetwork = true;
				return raceAwaitingLocalNetwork(once, {
					now: Date.now,
					sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
					stopped: () => !mounted.current,
					answeredAt: () => promptAnsweredAt.current,
					onWaiting: () => setAwaitingLocalNetwork(true),
				});
			},
			exchange: (endpoint, code) => exchangeForDeviceToken(endpoint, { code }, runtimeExchangeDeps()),
			verify: async (config) => void (await pingServer(config)),
			chooseName: (suggested, appearance, hostId) => new Promise<MachineNameChoice | null>((resolve) => {
				router.push(machineNameSheetRoute({ suggested, appearance, hostId, onSelect: resolve }));
			}),
			saveHost,
			setActiveHost,
			onStep: step,
		}, { repairHost: knownHost ?? undefined });

		setAwaitingLocalNetwork(false);
		if (!result.ok) {
			if (result.reason === "cancelled") {
				pendingCode.current = null;
				gate.cancelled();
				setBusy(false);
				return;
			}
			gate.failed(code, Date.now(), {
				awaitedLocalNetwork: awaitedLocalNetwork && result.reason === "none-reachable",
			});
			haptics.warning();
			if (result.reason === "wrong-machine") {
				setFailure({
					title: "Different computer",
					icon: "alert-circle",
					message: `That code belongs to a different computer. Scan the code from ${repairHost?.name ?? "the selected computer"}.`,
					showLocalNetworkHint: false,
				});
			} else if (result.reason === "name-required") {
				setFailure({
					title: "Name required",
					icon: "alert-circle",
					message: "Enter a name before saving this connection.",
					showLocalNetworkHint: false,
				});
			} else if (result.reason === "verify-failed") {
				// The race reached this endpoint, so whatever went wrong happened
				// there — name it, and let a 401 read as a rejected password.
				setFailure(
					describeConnectionFailure(classifyConnectionFailure(result.status, result.code), {
						host: result.endpoint.host,
						port: String(result.endpoint.port),
						platform: Platform.OS,
						name: repairHost?.name,
					}),
				);
			} else if (result.reason === "none-reachable" || result.reason === "no-candidates") {
				setFailure(describeUnreachableCode(offer?.endpoints ?? [], Platform.OS));
			} else {
				setFailure(
					describeConnectionFailure(
						result.reason === "not-tallpine-qr"
							? "not-tallpine-qr"
							: result.reason === "outdated-desktop"
								? "outdated-desktop"
								: result.reason === "incompatible"
									? "incompatible-host"
									: result.reason === "https-required"
										? "https-required"
									: classifyConnectionFailure(undefined),
						{ host: "", port: "", platform: Platform.OS },
					),
				);
			}
			setBusy(false);
			return;
		}

		gate.succeeded();
		if (isLocalNetworkHost(result.config.host)) void setLocalNetworkConfirmed();
		// Cleanup queued by an earlier "forget" of this machine belongs to the old
		// pairing; replaying it after this one would unpair the phone again.
		await dropMachineUnpairs(result.host.id).catch(() => {});
		// The rest of the app still runs off ServerConfig, so the winning
		// endpoint is written there as well as into the host list.
		await saveConfig(result.config);
		haptics.success();
		await completePairing(result.config);
		// A successful scan counts as a deliberate trigger for the one-shot OS
		// prompt (see `ask` on registerForPushNow in lib/push.ts): a granted device
		// registers with this machine now instead of waiting on the Settings switch.
		void registerForPush(result.config, { ask: true }).catch((e) => console.warn("[push] registration failed", e));
	}

	latestPair.current = (code) => void pair(code, parsePairingCode(code));

	function retry() {
		setFailure(null);
		rejected.current = null;
		if (pendingCode.current) {
			const offer = parsePairingCode(pendingCode.current);
			void pair(pendingCode.current, offer);
		}
	}

	return { leave, finish, completePairing, onScan, retry };
}
