import { Pressable, ScrollView, StyleSheet, Text } from "react-native";
import { haptics } from "./haptics";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { ALL_WORKER_LANES, type WorkerLaneChip } from "./worker-lane-filter";
import { press, radius, space, type } from "./tokens";

const CHIP_HEIGHT = 32;
const CHIP_HIT_SLOP = { top: 6, bottom: 6, left: 2, right: 2 };

/** Lane chip row for the Workers tab: "All lanes" plus one chip per lane. Filtering is the caller's. */
export function WorkerLaneFilter({
	chips,
	selectedLaneId,
	onSelectLane,
}: {
	chips: readonly WorkerLaneChip[];
	selectedLaneId: string;
	onSelectLane: (laneId: string) => void;
}) {
	const styles = useThemedStyles(makeStyles);
	const all: WorkerLaneChip = { id: ALL_WORKER_LANES, label: "All lanes" };
	return (
		<ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.row} style={styles.strip} testID="worker-lane-strip">
			{[all, ...chips].map((chip) => {
				const selected = chip.id === selectedLaneId;
				return (
					<Pressable
						key={chip.id}
						accessibilityRole="button"
						accessibilityState={{ selected }}
						hitSlop={CHIP_HIT_SLOP}
						onPress={() => {
							if (selected) return;
							haptics.tap();
							onSelectLane(chip.id);
						}}
						style={({ pressed }) => [styles.chip, selected && styles.selected, pressed && styles.pressed]}
						testID={`worker-lane-chip-${chip.id}`}
					>
						<Text numberOfLines={1} style={[styles.label, selected && styles.selectedLabel]}>{chip.label}</Text>
					</Pressable>
				);
			})}
		</ScrollView>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		strip: { flexGrow: 0 },
		row: { flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.lg, paddingBottom: space.sm },
		chip: { height: CHIP_HEIGHT, borderRadius: radius.pill, alignItems: "center", justifyContent: "center", paddingHorizontal: space.md, backgroundColor: t.bgElevated },
		selected: { backgroundColor: t.accent },
		pressed: { opacity: press.opacity },
		label: { ...type.footnote, fontFamily: "Geist_500Medium", fontWeight: "500", color: t.textSecondary },
		selectedLabel: { color: t.onAccent },
	});
