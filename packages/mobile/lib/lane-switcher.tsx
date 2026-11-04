import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import type { OrchestratorLink } from "./api";
import { haptics } from "./haptics";
import { laneLabel } from "./laneSelection";
import { orchestratorState } from "./orchestratorView";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, radius, space, type } from "./tokens";
import { Dot } from "./ui";

/**
 * Picks which of a project's orchestrators the screen (and chat, spawn and
 * send behind it) talks to. Each chip names the lane and shows whether its
 * orchestrator is running. Hidden for a project with one orchestrator, so
 * single-lane projects look exactly as before.
 */
export function LaneSwitcher({
	lanes,
	selectedId,
	onSelect,
}: {
	lanes: readonly OrchestratorLink[];
	selectedId: string | null | undefined;
	onSelect: (lane: OrchestratorLink) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	if (lanes.length < 2) return null;
	return (
		<ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.row} accessibilityRole="tablist">
			{lanes.map((lane) => {
				const running = orchestratorState(lane) === "running";
				const selected = lane.id === selectedId;
				return (
					<Pressable
						key={lane.id}
						accessibilityRole="tab"
						accessibilityState={{ selected }}
						accessibilityLabel={`${laneLabel(lane)} lane, ${running ? "running" : "stopped"}`}
						onPress={() => {
							haptics.select();
							onSelect(lane);
						}}
						style={[styles.chip, selected && styles.chipSelected]}
					>
						<Dot color={running ? t.green : t.textFaint} size={6} />
						<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={[styles.label, selected && styles.labelSelected]}>
							{laneLabel(lane)}
						</Text>
					</Pressable>
				);
			})}
		</ScrollView>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		row: { flexDirection: "row", gap: space.xs, paddingVertical: space.xs, paddingHorizontal: space.lg },
		chip: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.xs,
			minHeight: 36,
			paddingHorizontal: space.md,
			borderRadius: radius.pill,
			backgroundColor: t.bgSubtle,
		},
		chipSelected: { backgroundColor: t.accentTint },
		label: { ...type.footnote, color: t.textSecondary },
		labelSelected: { color: t.accent },
	});
