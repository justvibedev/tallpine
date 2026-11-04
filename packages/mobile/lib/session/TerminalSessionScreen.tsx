import { Feather } from "../icons";
import { XtermJsWebView } from "@fressh/react-native-xtermjs-webview";
import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useState } from "react";
import { Keyboard, Platform, Pressable, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { getPreview } from "../api";
import { isConfigured } from "../config";
import { terminalTheme } from "../theme";
import { haptics } from "../haptics";
import { resetHeaderRightForSwap } from "../headerRightSwap";
import { MinimalBackButton } from "../MinimalBackButton";
import { filesRoute } from "../files/navigation";
import { glassHeaderControl } from "../native-header-items";
import { Composer } from "./Composer";
import { dockInset, rootKeyboardPad } from "./keyboardInset";
import { KeyRow } from "./KeyRow";
import { type SendTarget } from "./sendRoute";
import { useApp } from "../store";
import { useVoiceInput } from "../voice/useVoiceInput";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { mobileInterfaceTransitionIsCancellable } from "./useInterfaceTransition";
import { adjustTerminalViewport } from "./terminalViewport";
import { isRuntimeGone, type RouteSession } from "./sessionRoute";
import { iconSize, press } from "../tokens";
import { backOr } from "../backNavigation";
import { TERMINAL_STATUS_LABEL } from "./terminalCopy";
import { TERMINAL_ENHANCE_JS } from "./terminalEnhanceScript";
import { statusColorFor, terminalInterfacePhaseLabel } from "./terminalPhase";
import { makeStyles } from "./terminalStyles";
import { TerminalHeaderActions } from "./TerminalHeaderActions";
import { useTerminalInterfaceSwitch } from "./useTerminalInterfaceSwitch";
import { useTerminalSessionActions } from "./useTerminalSessionActions";
import { useTerminalBridge } from "./useTerminalBridge";
import { useTerminalKeyboard } from "./useTerminalKeyboard";

const FONT_SIZE = 12;

/**
 * `session` is what the route resolved when the board's lists do not hold this
 * id (see `sessionRouteView`); the lists still win whenever they have it, since
 * they are refreshed on every poll. A session from that lookup is not refreshed.
 * The shell route passes nothing.
 */
export default function TerminalScreen({ session: resolved }: { session?: RouteSession }) {
	const t = useTheme();
	const { scheme, style } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const params = useLocalSearchParams<{ id?: string; handleId?: string; projectId?: string; sessionId?: string; title?: string; kind?: string; hostId?: string }>();
	const shellOnly = Boolean(params.handleId);
	// A reviewer pane is attached by handle like a shell, but the daemon does not
	// own it as a shell terminal: closing it means stopping the worker's reviewer.
	const reviewerPane = params.kind === "reviewer" && Boolean(params.sessionId);
	const id = String(params.handleId ?? params.id ?? "");
	const sessionId = shellOnly ? String(params.sessionId ?? "") : id;
	const projectId = params.projectId ? String(params.projectId) : undefined;
	const router = useRouter();
	const navigation = useNavigation();
	const [headerRightReady, setHeaderRightReady] = useState(false);
	useLayoutEffect(
		() => resetHeaderRightForSwap(
			() => navigation.setOptions(glassHeaderControl("right")),
			() => setHeaderRightReady(true),
		),
		[navigation],
	);
	const insets = useSafeAreaInsets();

	// Leaving the screen. This route is cold-started with no back stack often
	// enough — a reload while on the terminal, or a deep link — that the rule lives
	// in one place; see `backOr`.
	const leave = useCallback(() => backOr(router), [router]);


	const [msg, setMsg] = useState("");
	const [sending, setSending] = useState(false);
	const [sendTarget, setSendTarget] = useState<SendTarget>(shellOnly ? "terminal" : "agent");
	const [restoring, setRestoring] = useState(false);
	// Poll generated previews so the globe can advertise the optional App preview tab.
	const [preview, setPreview] = useState<{ entry: string; url: string } | null>(null);

	const { sessions, orchestrators, restore, refresh, config: activeConfig } = useApp();
	const known =
		sessions.find((s) => s.id === sessionId) ??
		orchestrators.find((o) => o.id === sessionId) ??
		(!shellOnly && resolved?.id === sessionId ? resolved : null);
	// Runtime handles are opaque. Native macOS PTYs are versioned (ptyhost-v1:),
	// so using the session id here would incorrectly route the attach to legacy
	// tmux. Older daemons omit terminalHandleId and retain the historical
	// session-id handle, which keeps the fallback backward-compatible.
	const terminalHandleId = shellOnly ? id : known?.terminalHandleId || id;
	const { xtermRef, muxRef, openedRef, lastDimsRef, status, size, banner, setBanner, notFound, setNotFound, logger, onInitialized, onData, sendKey } =
		useTerminalBridge({ activeConfig, terminalHandleId, projectId, scheme, style });
	const {
		interfaceSwitch, rechecking, interfaceTransitionActive, interfaceRecoveryMessage,
		interfaceTransitionNotice, interfaceTransitionNoticeText, interfaceFailureRecovery,
		requestInterfaceSwitch, retryInterfaceCheck, requestInterfaceFailureRecovery,
	} = useTerminalInterfaceSwitch({ activeConfig, shellOnly, sessionId, refresh, known, setBanner });
	const dead = notFound || (!shellOnly && known ? isRuntimeGone(known) : false);
	// What counts as a live preview: any file the daemon surfaces (an .html build, or
	// a generated doc like plan.md / a report) EXCEPT a repo's README, which the
	// detector's markdown fallback always matches on a fresh checkout. Filtering the
	// README out keeps the globe's green dot meaningful — it means "there's something
	// the agent produced to view", not just "this repo has a README".
	const previewBase = (preview?.entry ?? "").split("/").pop() ?? "";
	const isReadme = /^readme\.(md|markdown)$/i.test(previewBase);
	const hasPreview = !!preview && !isReadme;

	const { kbHeight, kbVisible } = useTerminalKeyboard();

	// Header shows just the short id; Kill lives in our own status bar below so we
	// fully control its shape/alignment (iOS draws its own box behind header
	// buttons, which fights any custom background).
	useLayoutEffect(() => {
		navigation.setOptions({
			title: shellOnly ? (params.title || "Worktree shell") : id.length > 22 ? `${id.slice(0, 20)}...` : id,
			// Always render our own Back control so it works even when the app was
			// cold-started directly on this route (reload/deep link) and the stack
			// has no history for the default back button to use.
			...glassHeaderControl("left", <MinimalBackButton onPress={leave} />),
		});
	}, [navigation, id, leave, params.title, shellOnly]);


	// Poll the daemon's on-demand preview detector while the terminal is open, just
	// to keep `preview` current for the globe button. We never auto-open the overlay
	// (see the note by the state above): the detector's markdown fallback matches a
	// repo README, so auto-popping would surface a blank/unbuilt page.
	useEffect(() => {
		if (shellOnly) return;
		if (!activeConfig || !isConfigured(activeConfig)) return;
		let cancelled = false;
		let timer: ReturnType<typeof setTimeout> | null = null;
		const tick = async () => {
			try {
				const p = await getPreview(activeConfig, id);
				if (cancelled) return;
				setPreview(p ? { entry: p.entry, url: p.url } : null);
			} catch {
				/* transient - keep polling */
			}
			if (!cancelled) timer = setTimeout(tick, 5000);
		};
		tick();
		return () => {
			cancelled = true;
			if (timer) clearTimeout(timer);
		};
	}, [activeConfig, id, shellOnly]);



	// Push-to-talk dictation, captured on the PHONE rather than by the harness.
	//
	// Driving a harness's own voice mode from here (Claude Code's /voice) looks
	// easy — the key row already sends arbitrary bytes to the PTY — but it records
	// through the *daemon host's* microphone, which is a machine the user is not
	// sitting at. It would also only ever work for the harnesses that ship a voice
	// mode at all.
	//
	// Capturing here inverts both: the transcript is just text, so it leaves
	// through sendPrompt like anything typed, and every harness gets it for free.
	const voice = useVoiceInput({
		onTranscript: useCallback((text: string) => {
			// Land in the composer as an editable draft rather than sending. This
			// text reaches an agent with tool access and speech-to-text mishears code
			// vocabulary often enough that silent submission isn't acceptable.
			//
			// The composer is always mounted now, so there is nothing to open — and
			// nothing focuses the field either, which would pop the keyboard over the
			// terminal mid-phrase.
			// Append, so several held phrases build one prompt (the same way holding
			// the key again appends in Claude Code's own dictation).
			setMsg((m) => (m ? `${m} ${text}` : text));
			haptics.success();
		}, []),
	});

	// Recognition failures surface in the existing banner rather than new UI.
	useEffect(() => {
		if (voice.error) setBanner(voice.error);
	}, [voice.error]);

	// Preview is one session-level destination: the live desktop browser first,
	// with the generated app/document preview available as a second tab.
	const toggleBrowser = useCallback(() => {
		haptics.tap();
		router.push({
			pathname: "/preview/[id]",
			params: {
				id: sessionId,
				hostId: params.hostId,
				previewUrl: known && "previewUrl" in known ? known.previewUrl ?? undefined : undefined,
			},
		});
	}, [known, params.hostId, router, sessionId]);


	// The browser toggle lives in the nav bar, beside the session name, to keep the
	// status row uncluttered. Separate from the headerLeft effect above because
	// `toggleBrowser` is declared here — referencing it in that effect's dep array
	// would read it before initialisation.
	useLayoutEffect(() => {
		if (shellOnly || !headerRightReady) {
			navigation.setOptions(glassHeaderControl("right"));
			return;
		}
		navigation.setOptions({
			headerRight: () => (
				<TerminalHeaderActions
					t={t} styles={styles} rechecking={rechecking} starting={interfaceSwitch.starting}
					transitionActive={interfaceTransitionActive} interfaceSupported={interfaceSwitch.status?.supported}
					hasPreview={hasPreview} onInterface={() => void requestInterfaceSwitch()} onPreview={toggleBrowser}
					onFiles={() => { haptics.tap(); router.push(filesRoute(sessionId, params.hostId, params.title)); }}
				/>
			),
		});
	}, [headerRightReady, navigation, hasPreview, toggleBrowser, styles, t, shellOnly, interfaceTransitionActive, interfaceSwitch.starting, rechecking, interfaceSwitch.status?.supported, requestInterfaceSwitch, router, sessionId, params.hostId, params.title]);



	const { sendPrompt, confirmKill, onRestore } = useTerminalSessionActions({
		activeConfig, id, terminalHandleId, projectId, sessionId: params.sessionId, reviewerPane, shellOnly, leave, restore,
		msg, setMsg, sendTarget, setSendTarget, setSending, setRestoring, status, setBanner, setNotFound,
		muxRef, openedRef, lastDimsRef,
	});

	const xtermOptions = useMemo(
		() => ({
			fontSize: FONT_SIZE,
			cursorBlink: true,
			scrollback: 5000,
			// Move more rows per swipe so touch scrolling feels responsive.
			scrollSensitivity: 3,
			fastScrollSensitivity: 8,
			// The terminal's own palette, not the product one: agent TUIs own the
			// meaning of the ANSI slots.
			theme: terminalTheme(scheme, style),
			// Agent TUIs pick their colours assuming a dark canvas, so on the light
			// theme they can land white-on-white — the prompt row is exactly that
			// once its background band is collapsed away. xterm nudges any pair that
			// falls below this ratio until it is readable, which is cheaper and far
			// more reliable than trying to anticipate every colour a TUI might emit.
			minimumContrastRatio: 4.5,
		}),
		// `scheme` and `style` matter: without them the terminal keeps the palette
		// it was built with and stays dark after a theme switch.
		[scheme, style],
	);

	// Adjust only the phone's CSS viewport. The xterm renderer, mux attachment,
	// replay buffer, and daemon-owned PTY dimensions all remain untouched.
	const zoom = useCallback((delta: number) => {
		adjustTerminalViewport(xtermRef.current, delta > 0 ? 1 : -1);
	}, []);

	const webViewOptions = useMemo(
		() => ({
			// Removes the extra "< > Done" / autofill bar iOS shows above the keyboard.
			hideKeyboardAccessoryView: true,
			// Custom drag/momentum scroll + input hardening (see TERMINAL_ENHANCE_JS).
			// Prepend the platform flag the enhance script branches on for scrolling.
			injectedJavaScript: `var IS_ANDROID=${Platform.OS === "android"};\n${TERMINAL_ENHANCE_JS}`,
			// NOTE: do NOT force androidLayerType:"hardware" here. xterm renders into a
			// <canvas>, and a hardware layer makes the Android WebView's render process
			// composite/crash blank on many devices (black terminal, no dims ever
			// reported). Leaving it at the default keeps the canvas visible.
			nestedScrollEnabled: true,
			// Surface an Android WebView render-process crash instead of a silent black
			// screen, so the user can tell the terminal died vs. never loaded.
			onRenderProcessGone: () => setBanner("The terminal view stopped working. Go back and open the session again."),
		}),
		[],
	);

	if (activeConfig && !isConfigured(activeConfig)) {
		return (
			<View style={styles.center}>
				<Text style={styles.bannerText}>No computer paired.</Text>
			</View>
		);
	}

	// One inset for the whole dock. The root already reserves the keyboard, so the
	// dock owes nothing more while the keyboard is up — see dockInset.
	const bottomPad = dockInset(kbHeight, insets.bottom);
	// Android's reported kbHeight excludes the nav bar our root view draws under,
	// so rootKeyboardPad adds it back; on iOS it passes the height through.
	const rootPad = rootKeyboardPad(Platform.OS === "android" ? "android" : "ios", kbHeight, insets.bottom);

	return (
		<View style={[styles.screen, rootPad > 0 && { paddingBottom: rootPad }]}>
			<View style={styles.statusBar}>
				<View style={[styles.statusDot, { backgroundColor: statusColorFor(t)[status] }]} />
				{/* Status-bar chrome: capped so the toolbar keeps its height and the
				    terminal grid keeps its rows at accessibility text sizes. */}
				<Text style={styles.statusText} numberOfLines={1} maxFontSizeMultiplier={1.4}>
					{TERMINAL_STATUS_LABEL[status]}
				</Text>
				{size && !dead && (
					<Text style={styles.dims} numberOfLines={1} maxFontSizeMultiplier={1.4}>
						{size.cols}x{size.rows}
					</Text>
				)}
				{/* Zoom only the mobile viewport; the shared PTY grid stays unchanged. */}
				{!dead && (
					<View style={styles.zoomGroup}>
						<Pressable
							hitSlop={6}
							accessibilityLabel="Smaller text"
							onPress={() => { haptics.tap(); zoom(-1); }}
							style={({ pressed }) => [styles.zoomBtn, pressed && { opacity: press.opacity }]}
						>
							<Feather name="minus" size={iconSize.xs} color={t.textSecondary} />
						</Pressable>
						<View style={styles.zoomDivider} />
						<Pressable
							hitSlop={6}
							accessibilityLabel="Larger text"
							onPress={() => { haptics.tap(); zoom(1); }}
							style={({ pressed }) => [styles.zoomBtn, pressed && { opacity: press.opacity }]}
						>
							<Feather name="plus" size={iconSize.xs} color={t.textSecondary} />
						</Pressable>
					</View>
				)}
				{dead && !shellOnly ? (
					<Pressable
						hitSlop={8}
						onPress={() => { haptics.tap(); void onRestore(); }}
						disabled={restoring}
						style={({ pressed }) => [styles.restoreBtn, (pressed || restoring) && { opacity: 0.7 }]}
					>
						<Feather name="rotate-ccw" size={iconSize.xs} color={t.accent} />
						<Text style={styles.restoreText}>{restoring ? "Restoring…" : "Restore"}</Text>
					</Pressable>
				) : (
					// Icon-only: the archive glyph matches the restorable action on the
					// workers board. The accessible label carries the full operation.
					<Pressable
						hitSlop={8}
						accessibilityLabel={shellOnly ? "Close shell" : "Archive session"}
						onPress={confirmKill}
						style={({ pressed }) => [styles.killBtn, pressed && { opacity: press.opacity }]}
					>
						<Feather name={shellOnly ? "x" : "archive"} size={iconSize.sm} color={shellOnly ? t.red : t.textSecondary} />
					</Pressable>
				)}
			</View>

			{banner && (
				<Pressable onPress={() => { haptics.tap(); setBanner(null); }} style={styles.banner}>
					<Text style={styles.bannerText}>{banner} (tap to dismiss)</Text>
				</Pressable>
			)}
			{interfaceTransitionNotice ? (
				<View style={styles.banner}>
					<Text style={styles.bannerText}>
						{interfaceTransitionNoticeText}
						{interfaceSwitch.acknowledgeNoticeError
							? ` Couldn't dismiss: ${interfaceSwitch.acknowledgeNoticeError}`
							: ""}
					</Text>
					<View style={styles.interfaceFailureActions}>
						{interfaceFailureRecovery ? (
							<Pressable
								accessibilityRole="button"
								onPress={() => {
									haptics.tap();
									requestInterfaceFailureRecovery();
								}}
							>
								<Text style={styles.interfaceFailureActionText}>{interfaceFailureRecovery.actionLabel}</Text>
							</Pressable>
						) : null}
						<Pressable
							accessibilityRole="button"
							accessibilityLabel="Dismiss interface switch error"
							disabled={interfaceSwitch.acknowledgingNotice}
							onPress={() => {
								haptics.tap();
								void interfaceSwitch
									.acknowledgeNotice(interfaceTransitionNotice.id)
									.catch(() => {});
							}}
						>
							<Text style={styles.interfaceFailureDismissText}>
								{interfaceSwitch.acknowledgingNotice ? "Dismissing…" : "Dismiss"}
							</Text>
						</Pressable>
					</View>
				</View>
			) : null}

			<View style={styles.termWrap}>
				<XtermJsWebView
					// Remount only on theme changes: xterm applies its palette at
					// construction, so already-painted rows otherwise keep old colours.
					key={`term-${style}-${scheme}`}
					ref={xtermRef}
					autoFit={false}
					xtermOptions={xtermOptions}
					webViewOptions={webViewOptions}
					logger={logger}
					onInitialized={onInitialized}
					onData={onData}
					style={{ flex: 1, backgroundColor: t.bgBase }}
				/>
				{interfaceTransitionActive ? (
					<View style={styles.interfaceOverlay}>
						<View style={styles.interfaceCard}>
							<Feather name={interfaceRecoveryMessage ? "alert-triangle" : "repeat"} size={iconSize.lg} color={t.accent} />
							<Text style={styles.interfaceTitle}>{interfaceRecoveryMessage ? "Interface recovery blocked" : "Switching to Chat"}</Text>
							<Text style={styles.interfaceCopy}>{interfaceRecoveryMessage || terminalInterfacePhaseLabel(interfaceSwitch.transition?.phase)}</Text>
							{mobileInterfaceTransitionIsCancellable(interfaceSwitch.transition) ? (
								<Pressable
									disabled={interfaceSwitch.cancelling}
									onPress={() => { haptics.tap(); void interfaceSwitch.cancel().catch(() => {}); }}
									style={styles.interfaceCancel}
								>
									<Text style={styles.interfaceCancelText}>{interfaceSwitch.cancelling ? "Cancelling…" : "Cancel switch"}</Text>
								</Pressable>
							) : null}
							{interfaceSwitch.error ? <Text style={styles.interfaceError}>{interfaceSwitch.error}</Text> : null}
							{interfaceSwitch.fetchFailed || interfaceRecoveryMessage ? (
								<Pressable
									disabled={rechecking}
									onPress={() => void retryInterfaceCheck()}
									style={styles.interfaceCancel}
								>
									<Text style={styles.interfaceCancelText}>{rechecking ? "Retrying…" : "Retry"}</Text>
								</Pressable>
							) : null}
						</View>
					</View>
				) : null}
				{dead && (
					<View style={styles.deadOverlay}>
						<View style={styles.deadIcon}>
							<Feather name="power" size={iconSize.xl} color={t.textTertiary} />
						</View>
						<Text style={styles.deadTitle}>{shellOnly ? "Shell closed" : "Session terminated"}</Text>
						<Text style={styles.deadMsg}>{shellOnly ? "This worktree shell is no longer running." : "This session has no live terminal. Restore it to bring the agent back."}</Text>
						{!shellOnly ? <Pressable
								onPress={() => { haptics.tap(); void onRestore(); }}
							disabled={restoring}
							style={({ pressed }) => [styles.restoreCta, (pressed || restoring) && { opacity: 0.8 }]}
						>
							<Feather name="rotate-ccw" size={iconSize.sm} color={t.onAccent} />
							<Text style={styles.restoreCtaText}>{restoring ? "Restoring…" : "Restore session"}</Text>
						</Pressable> : null}
					</View>
				)}
			</View>

			{/* The input dock. One container, one bottom inset, fixed slots — every
			    control sits in the same place in every state the screen can reach. */}
			<View style={[styles.dock, { paddingBottom: bottomPad }]}>
				{/* Live dictation readout. Deliberately not inside the field: the
				    partial transcript changes on every syllable, and rewriting the
				    input under the user's caret is hostile. */}
				{(voice.state === "starting" || voice.state === "recording" || voice.state === "transcribing") && (
					<View style={[styles.voiceStrip, voice.state === "starting" && styles.voiceStripWarmup]}>
						<Feather name="mic" size={iconSize.xs} color={voice.state === "starting" ? t.textTertiary : t.accent} />
						<Text style={styles.voiceText} numberOfLines={2}>
							{voice.partial ||
								(voice.state === "starting"
									? // The mic is not capturing yet. Saying "Listening" here would
										// invite speech that gets dropped during warm-up.
										"Keep holding…"
									: voice.state === "transcribing"
										? "Transcribing…"
										: voice.mode === "latched"
											? "Recording hands-free — tap the mic to finish"
											: "Speak now — release to insert")}
						</Text>
					</View>
				)}

				<KeyRow onKey={sendKey} />

				<Composer
					value={msg}
					onChangeText={setMsg}
					onSend={sendPrompt}
					sending={sending || interfaceTransitionActive}
					target={sendTarget}
					onTargetChange={setSendTarget}
					voice={voice}
					keyboardVisible={kbVisible}
					onDismissKeyboard={Keyboard.dismiss}
					targetLocked={shellOnly}
				/>
			</View>
		</View>
	);
}

