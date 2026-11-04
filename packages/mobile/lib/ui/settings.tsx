import { Feather } from "../icons";
import { Children, type ReactNode } from "react";
import { ActivityIndicator, Animated, Pressable, Switch, Text, View, type StyleProp, type ViewStyle } from "react-native";
import { haptics } from "../haptics";
import { usePressScale } from "../motionHooks";
import { fontScaleCap } from "../tokens";
import { useTheme, useThemedStyles } from "../ThemeProvider";


import { makeStyles } from "./styles";

// One titled group of rows in a single rounded container. `footer` carries the
// explanatory prose that used to sit inline above the controls.
export function SettingsGroup({
	title,
	footer,
	children,
	style,
}: {
	title?: string;
	footer?: string;
	children: ReactNode;
	style?: StyleProp<ViewStyle>;
}) {
	const s = useThemedStyles(makeStyles);
	// Separators belong *between* rows, so they're injected here rather than
	// drawn by each row — a row can't know whether it is the last one.
	// Children.toArray flattens fragments, drops nulls from conditional rows, and
	// hands back stable keys, so a conditionally-rendered row can't shift them.
	const rows = Children.toArray(children);
	return (
		<View style={[s.group, style]}>
			{title ? <Text style={s.groupTitle}>{title.toUpperCase()}</Text> : null}
			<View style={s.groupBody}>
				{rows.map((row, i) => (
					<View key={(row as { key?: string }).key ?? i}>
						{i > 0 ? <View style={s.separator} /> : null}
						{row}
					</View>
				))}
			</View>
			{footer ? <Text style={s.groupFooter}>{footer}</Text> : null}
		</View>
	);
}

// A single row: icon, label, right-aligned value, chevron when tappable.
// `right` replaces the value/chevron entirely (used by SettingsToggle).
export function SettingsRow({
	icon,
	label,
	value,
	valueColor,
	leading,
	onPress,
	destructive = false,
	loading = false,
	disabled = false,
	right,
}: {
	icon?: keyof typeof Feather.glyphMap;
	label: string;
	value?: string;
	valueColor?: string;
	// Rendered immediately before the value — the connection dot lives here.
	leading?: ReactNode;
	onPress?: () => void;
	destructive?: boolean;
	loading?: boolean;
	disabled?: boolean;
	right?: ReactNode;
}) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const labelColor = destructive ? t.red : t.textPrimary;
	const iconColor = destructive ? t.red : t.textSecondary;
	const pressFx = usePressScale();
	const body = (
		<>
			{icon ? <Feather name={icon} size={17} color={iconColor} style={s.rowIcon} /> : null}
			<Text
				style={[s.rowLabel, { color: labelColor }]}
				numberOfLines={1}
				maxFontSizeMultiplier={fontScaleCap.body}
			>
				{label}
			</Text>
			{/* The accessory never shrinks: at large text sizes the label has to be the
			    one that gives way, or it draws underneath the switch. */}
			<View style={s.rowAccessory}>
				{right ?? (
					<>
						{loading ? <ActivityIndicator size="small" color={t.textTertiary} /> : null}
						{!loading && leading ? leading : null}
						{!loading && value ? (
							<Text
								style={[s.rowValue, valueColor ? { color: valueColor } : null]}
								numberOfLines={1}
								maxFontSizeMultiplier={fontScaleCap.chrome}
							>
								{value}
							</Text>
						) : null}
						{onPress ? <Feather name="chevron-right" size={17} color={t.textFaint} style={s.rowChevron} /> : null}
					</>
				)}
			</View>
		</>
	);

	if (!onPress) return <View style={[s.row, disabled && s.rowDisabled]}>{body}</View>;
	return (
		<Animated.View style={pressFx.style}>
			<Pressable
				accessibilityRole="button"
				// No `accessibilityLabel`: the row's own text is the name, and pinning one
				// would drop the accessory value ("Connected", "2 devices") from it.
				accessibilityState={{ disabled: disabled || loading, busy: loading }}
				disabled={disabled || loading}
				onPressIn={pressFx.onPressIn}
				onPressOut={pressFx.onPressOut}
				onPress={() => {
					if (destructive) haptics.warning();
					else haptics.tap();
					onPress();
				}}
				style={({ pressed }) => [s.row, pressed && s.rowPressed, (disabled || loading) && s.rowDisabled]}
			>
				{body}
			</Pressable>
		</Animated.View>
	);
}

// A row whose accessory is a switch. Same metrics as SettingsRow so the two
// line up inside one group.
export function SettingsToggle({
	icon,
	label,
	value,
	onValueChange,
	disabled = false,
	busy = false,
}: {
	icon?: keyof typeof Feather.glyphMap;
	label: string;
	value: boolean;
	onValueChange: (v: boolean) => void;
	disabled?: boolean;
	busy?: boolean;
}) {
	const t = useTheme();
	return (
		<SettingsRow
			icon={icon}
			label={label}
			disabled={disabled}
			right={
				busy ? (
					<ActivityIndicator size="small" color={t.textTertiary} />
				) : (
					<Switch
						value={value}
						onValueChange={(v) => {
							haptics.select();
							onValueChange(v);
						}}
						disabled={disabled}
						trackColor={{ true: t.accent, false: t.borderStrong }}
					/>
				)
			}
		/>
	);
}
