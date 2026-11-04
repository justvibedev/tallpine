import * as Clipboard from "expo-clipboard";
import { useState } from "react";
import { ActivityIndicator, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { PRODUCT_NAME } from "./brand";
import { haptics } from "./haptics";
import { Feather } from "./icons";
import { formatPairDetails, PAIR_STEPS, type PairProgress, type PairStepState } from "./pairProgress";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, space, type } from "./tokens";

export type PairPanelAction = { icon: "refresh-cw" | "chevron-right"; title: string; onPress: () => void };

/**
 * The pairing steps as they happen, under the viewfinder, plus "Copy details"
 * so a stalled pairing can be shared. Replaces the single "Connecting…" line,
 * which could not say where a pairing stopped.
 */
export function PairProgressPanel({ progress, actions = [], hint }: {
	progress: PairProgress;
	actions?: PairPanelAction[];
	/** One line under the steps, e.g. the Local Network prompt hint. */
	hint?: string;
}) {
	const styles = useThemedStyles(makeStyles);
	const [copied, setCopied] = useState(false);
	async function copy() {
		haptics.tap();
		await Clipboard.setStringAsync(formatPairDetails(progress, { app: PRODUCT_NAME, platform: Platform.OS }));
		setCopied(true);
	}
	return (
		<View style={styles.panel} accessibilityLiveRegion="polite" testID="pair-progress">
			{PAIR_STEPS.map((step) => {
				const state = progress.steps[step.id];
				const label = state === "done" ? step.done : state === "failed" ? step.failed : step.active;
				return (
					<View key={step.id} style={styles.row} testID={`pair-step-${step.id}-${state}`}>
						<View style={styles.lead}><StepIcon state={state} /></View>
						<Text style={[styles.label, state === "pending" && styles.pending, state === "failed" && styles.failed]}>{label}</Text>
					</View>
				);
			})}
			{hint ? <Text style={styles.hint}>{hint}</Text> : null}
			<View style={styles.actions}>
				{actions.map((a) => <PanelAction key={a.title} icon={a.icon} title={a.title} onPress={a.onPress} />)}
				<PanelAction icon={copied ? "check" : "copy"} title={copied ? "Copied" : "Copy details"} onPress={() => void copy()} />
			</View>
		</View>
	);
}

function StepIcon({ state }: { state: PairStepState }) {
	const t = useTheme();
	if (state === "active") return <ActivityIndicator size="small" color={t.textSecondary} />;
	if (state === "done") return <Feather name="check" size={iconSize.sm} color={t.green} />;
	if (state === "failed") return <Feather name="alert-circle" size={iconSize.sm} color={t.red} />;
	return <Feather name="circle" size={iconSize.sm} color={t.textTertiary} />;
}

function PanelAction({ icon, title, onPress }: { icon: "refresh-cw" | "chevron-right" | "copy" | "check"; title: string; onPress: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<Pressable
			onPress={() => { haptics.tap(); onPress(); }}
			style={({ pressed }) => [styles.action, pressed && { opacity: 0.6 }]}
			accessibilityRole="button"
			accessibilityLabel={title}
			hitSlop={8}
		>
			<Feather name={icon} size={iconSize.sm} color={t.textPrimary} />
			<Text style={styles.actionText}>{title}</Text>
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	panel: { paddingHorizontal: space.xl, marginTop: space.lg, gap: space.xs },
	row: { flexDirection: "row", alignItems: "center", gap: space.sm },
	lead: { width: iconSize.sm, height: type.subheadline.lineHeight, alignItems: "center", justifyContent: "center" },
	label: { flex: 1, fontFamily: "Geist_400Regular", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	pending: { color: t.textTertiary },
	failed: { color: t.red },
	hint: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginLeft: iconSize.sm + space.sm },
	actions: { flexDirection: "row", flexWrap: "wrap", gap: space.lg, marginTop: space.sm, marginLeft: iconSize.sm + space.sm },
	action: { flexDirection: "row", alignItems: "center", gap: space.xs },
	actionText: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.footnote.fontSize, fontWeight: "600" },
});
