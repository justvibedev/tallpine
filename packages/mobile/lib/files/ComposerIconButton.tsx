import { Pressable } from "react-native";
import { Feather, type FeatherIconName } from "../icons";
import { useTheme } from "../ThemeProvider";
import { iconSize, press, space } from "../tokens";

/** A 44pt icon button in the selection composer's header. */
export function ComposerIconButton({ label, icon, onPress }: { label: string; icon: FeatherIconName; onPress: () => void }) {
	const t = useTheme();
	return (
		<Pressable accessibilityRole="button" accessibilityLabel={label} hitSlop={6} onPress={onPress} style={({ pressed }) => [{ width: 44, height: 44, alignItems: "center", justifyContent: "center", marginLeft: -space.sm }, pressed && { opacity: press.opacity }]}>
			<Feather name={icon} size={iconSize.md} color={t.textSecondary} />
		</Pressable>
	);
}
