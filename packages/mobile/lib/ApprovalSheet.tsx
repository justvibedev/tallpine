import { useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { ActivityIndicator, ScrollView, StyleSheet, Text, View } from "react-native";
import { backOr } from "./backNavigation";
import { approvalSheetModel, approvalSheetShouldOpenSession, type ApprovalSheetModel } from "./approvalSheetModel";
import { getConversationPage, resolveApproval } from "./chat/api";
import { userFacingError } from "./connectionError";
import { haptics } from "./haptics";
import { useApp } from "./store";
import { textSelectionColors } from "./textSelection";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, radius, space, type } from "./tokens";
import { Button, SheetScreen } from "./ui";

const HOST_WAIT_MS = 15000;

/**
 * Answers a worker's permission prompt without opening its chat. Opened from an
 * Inbox row or a needs-input push; shows the exact command and the daemon's own
 * decisions. With nothing to answer (no open prompt, or one the phone cannot
 * present) it opens the session instead, so the tap still lands somewhere useful.
 */
export function ApprovalSheet({ sessionId, hostId }: { sessionId: string; hostId: string }) {
	const router = useRouter();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { configForHost, hostStates } = useApp();
	const [model, setModel] = useState<ApprovalSheetModel | null | undefined>(undefined);
	const [sending, setSending] = useState<string>();
	const [error, setError] = useState<string>();
	// hostStates changes when the machine hydrates, which is what re-renders this
	// once the config exists; configForHost alone reads a ref and would not.
	const host = hostStates.find((candidate) => candidate.hostId === hostId);
	const config = host?.config ?? configForHost(hostId);
	const [waitedOut, setWaitedOut] = useState(false);
	useEffect(() => {
		const timer = setTimeout(() => setWaitedOut(true), HOST_WAIT_MS);
		return () => clearTimeout(timer);
	}, []);

	useEffect(() => {
		if (!config) return;
		let cancelled = false;
		getConversationPage(config, sessionId).then(
			(snapshot) => !cancelled && setModel(approvalSheetModel(snapshot)),
			() => !cancelled && setModel(null),
		);
		return () => {
			cancelled = true;
		};
		// The config object is rebuilt on refresh; the machine and session identify the prompt.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [sessionId, hostId, !!config]);

	useEffect(() => {
		if (approvalSheetShouldOpenSession({ model, hasConfig: !!config, hostLoading: !!host?.loading, hostKnown: !!host, waitedOut })) {
			router.replace({ pathname: "/session/[id]", params: { id: sessionId, hostId } });
		}
	}, [model, config, host, waitedOut, router, sessionId, hostId]);

	if (!model || !config) {
		return (
			<SheetScreen title="Approval needed">
				<View style={styles.center}>
					<ActivityIndicator color={t.accent} />
				</View>
			</SheetScreen>
		);
	}

	const decide = async (decisionId: string) => {
		setSending(decisionId);
		setError(undefined);
		try {
			await resolveApproval(config, sessionId, model.requestId, decisionId);
			haptics.success();
			backOr(router);
		} catch (cause) {
			haptics.error();
			setError(userFacingError(cause, "Couldn't send your answer."));
			setSending(undefined);
		}
	};

	return (
		<SheetScreen title="Approval needed" subtitle={model.reason}>
			<ScrollView contentContainerStyle={styles.body}>
				<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.label}>
					Command
				</Text>
				<View style={styles.commandSurface}>
					<Text selectable {...textSelectionColors(t)} style={styles.command}>
						{model.command}
					</Text>
				</View>
				{model.cwd ? (
					<Text selectable style={styles.meta}>
						{`in ${model.cwd}`}
					</Text>
				) : null}
				{error ? (
					<Text accessibilityRole="alert" style={styles.error}>
						{error}
					</Text>
				) : null}
				<View style={styles.actions}>
					{model.decisions.map((decision, index) => (
						<Button
							key={decision.id}
							title={sending === decision.id ? "Sending…" : decision.label}
							variant={index === 0 ? "primary" : "ghost"}
							disabled={Boolean(sending)}
							onPress={() => void decide(decision.id)}
						/>
					))}
				</View>
			</ScrollView>
		</SheetScreen>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		center: { padding: space.xxl, alignItems: "center" },
		body: { gap: space.sm, paddingHorizontal: space.xl, paddingBottom: space.xxl },
		label: { ...type.caption1, color: t.textTertiary },
		commandSurface: { padding: space.md, borderRadius: radius.md, backgroundColor: t.bgSubtle },
		command: { ...type.footnote, fontFamily: t.fontMono, color: t.textPrimary },
		meta: { ...type.footnote, color: t.textTertiary },
		error: { ...type.footnote, color: t.red },
		actions: { gap: space.sm, marginTop: space.md },
	});
