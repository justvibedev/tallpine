import { Feather } from "../icons";
import { ActivityIndicator, Animated, Pressable, Text, View, type StyleProp, type ViewStyle } from "react-native";
import { haptics } from "../haptics";
import { usePressScale } from "../motionHooks";
import { fontScaleCap, press, space } from "../tokens";
import { useTheme, useThemedStyles } from "../ThemeProvider";


import { makeStyles } from "./styles";

export function Button({
	title,
	onPress,
	variant = "primary",
	loading = false,
	disabled = false,
	icon,
	style,
}: {
	title: string;
	onPress: () => void;
	/** `success` is desktop's green merge action. */
	variant?: "primary" | "ghost" | "danger" | "success";
	loading?: boolean;
	disabled?: boolean;
	icon?: keyof typeof Feather.glyphMap;
	style?: StyleProp<ViewStyle>;
}) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const isPrimary = variant === "primary";
	const isDanger = variant === "danger";
	const isSuccess = variant === "success";
	// `onAccent`, not a literal: near-black reads best on the dark theme's light
	// accent, but is invisible on light mode's darker one, which needs white.
	const fg = isPrimary ? t.onAccent : isDanger ? t.red : isSuccess ? t.bgBase : t.accent;
	const pressFx = usePressScale(disabled || loading);
	return (
		<Animated.View style={[style, pressFx.style]}>
			<Pressable
				accessibilityRole="button"
				// The label is pinned to the visible title rather than left to the child
				// text: while a request is in flight the text is replaced by a spinner,
				// and the control would otherwise announce with no name at all.
				accessibilityLabel={title}
				accessibilityState={{ disabled: disabled || loading, busy: loading }}
				onPressIn={pressFx.onPressIn}
				onPressOut={pressFx.onPressOut}
				onPress={() => {
					// Danger actions get a cautionary buzz; everything else a light tap.
					if (isDanger) haptics.warning();
					else haptics.tap();
					onPress();
				}}
				disabled={disabled || loading}
				style={({ pressed }) => [
					s.btn,
					isPrimary && s.btnPrimary,
					!isPrimary && s.btnGhost,
					isDanger && s.btnDanger,
					isSuccess && s.btnSuccess,
					(disabled || loading) && { opacity: 0.5 },
					pressed && { opacity: press.opacity },
				]}
			>
				{loading ? (
					<ActivityIndicator color={fg} size="small" />
				) : (
					<View style={s.btnInner}>
						{icon ? <Feather name={icon} size={15} color={fg} style={{ marginRight: space.xs }} /> : null}
						<Text maxFontSizeMultiplier={fontScaleCap.body} style={[s.btnText, { color: fg }]}>
							{title}
						</Text>
					</View>
				)}
			</Pressable>
		</Animated.View>
	);
}

// A numbered instruction row. Used by both onboarding screens: the welcome
// screen passes a `hint` for the full "how it works" list, the scanner omits it
// so the steps stay one line each above the viewfinder.
export function NumberedStep({
	n,
	title,
	hint,
	compact = false,
}: {
	n: number;
	title: string;
	hint?: string;
	compact?: boolean;
}) {
	const s = useThemedStyles(makeStyles);
	return (
		<View style={[s.step, compact && s.stepCompact]}>
			<View style={[s.stepBadge, compact && s.stepBadgeCompact]}>
				<Text style={[s.stepNum, compact && s.stepNumCompact]}>{n}</Text>
			</View>
			<View style={{ flex: 1 }}>
				<Text style={[s.stepTitle, compact && s.stepTitleCompact]}>{title}</Text>
				{hint ? <Text style={s.stepHint}>{hint}</Text> : null}
			</View>
		</View>
	);
}

/**
 * A compact square action, for the cluster that sits bottom-right on a card.
 *
 * Cards carry two or three of these, so they are icon-only: a row of labelled
 * buttons would dominate a card whose point is the content above it.
 */
export function IconButton({
	icon,
	label,
	onPress,
	destructive = false,
	disabled = false,
	loading = false,
}: {
	icon: keyof typeof Feather.glyphMap;
	/** Required — the control has no visible text of its own. */
	label: string;
	onPress: () => void;
	destructive?: boolean;
	disabled?: boolean;
	loading?: boolean;
}) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const pressFx = usePressScale(disabled || loading);
	return (
		<Animated.View style={pressFx.style}>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={label}
				accessibilityState={{ disabled: disabled || loading }}
				disabled={disabled || loading}
				hitSlop={6}
				onPressIn={pressFx.onPressIn}
				onPressOut={pressFx.onPressOut}
				onPress={() => {
					if (destructive) haptics.warning();
					else haptics.tap();
					onPress();
				}}
				style={({ pressed }) => [
					s.iconBtn,
					destructive && { borderColor: t.tintRed },
					pressed && (destructive ? s.iconBtnPressedDanger : s.iconBtnPressed),
					(disabled || loading) && { opacity: 0.4 },
				]}
			>
				{loading ? (
					<ActivityIndicator size="small" color={t.textSecondary} />
				) : (
					<Feather name={icon} size={15} color={destructive ? t.red : t.textSecondary} />
				)}
			</Pressable>
		</Animated.View>
	);
}
