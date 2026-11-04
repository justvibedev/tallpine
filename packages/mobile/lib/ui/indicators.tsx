import { Feather } from "../icons";
import { memo, useEffect, useRef, type ReactNode } from "react";
import { Animated, Easing, Pressable, Text, View, type StyleProp, type TextStyle, type ViewStyle } from "react-native";
import { haptics } from "../haptics";
import { BREATHE_MS, shouldBreathe, shouldSpin, SPIN_MS } from "../motion";
import { useReducedMotion } from "../useReducedMotion";
import { fontScaleCap, space, type } from "../tokens";
import { statusVisual } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";


import { makeStyles } from "./styles";

/**
 * The app's breathing loop, in one place.
 *
 * "Working" is the only state that moves, and it moves the same way wherever it
 * appears: a slow opacity pulse. Returns the animated value to drive it, or
 * `undefined` when the loop must not run — which is every state that is not
 * working, and everything at all under Reduce Motion.
 *
 * The setting is consumed here rather than at the call sites, so honouring it
 * once fixes every consumer — status badges, the connection lamp, project rows —
 * without touching any of them.
 */
export function useBreathing(enabled: boolean): Animated.Value | undefined {
	const pulse = useRef(new Animated.Value(1)).current;
	const reduceMotion = useReducedMotion();
	const animate = shouldBreathe(reduceMotion, enabled);
	useEffect(() => {
		// Deliberately not a zero duration: a zero-length loop is a busy loop, so
		// the animation must not start at all.
		if (!animate) return;
		const loop = Animated.loop(
			Animated.sequence([
				Animated.timing(pulse, {
					toValue: 0.35,
					duration: BREATHE_MS,
					useNativeDriver: true,
				}),
				Animated.timing(pulse, {
					toValue: 1,
					duration: BREATHE_MS,
					useNativeDriver: true,
				}),
			]),
		);
		loop.start();
		return () => {
			loop.stop();
			// Leave it at full opacity; a stopped loop otherwise freezes mid-fade,
			// which reads as a rendering bug rather than a resting state.
			pulse.setValue(1);
		};
	}, [animate, pulse]);
	return animate ? pulse : undefined;
}

// A gently breathing dot - the only motion in the UI, reserved for "working".
// Memoized so an unrelated parent re-render doesn't tear down and restart the
// Animated loop (which causes a visible flicker and per-tick allocations).
export const Dot = memo(function Dot({
	color,
	size = 9,
	breathing = false,
}: {
	color: string;
	size?: number;
	breathing?: boolean;
}) {
	const pulse = useBreathing(breathing);
	return (
		<Animated.View
			style={{
				width: size,
				height: size,
				borderRadius: size / 2,
				backgroundColor: color,
				opacity: pulse ?? 1,
			}}
		/>
	);
});

/**
 * A continuous turn, for a glyph that means "in progress".
 *
 * A circle drawn still reads as stopped, which is the one thing a progress
 * indicator must not say — and a pulse is a different signal: it reads as waiting
 * or attention, not as work happening. Linear and endless, so nothing about it
 * suggests it is about to finish. Returning a fragment when the loop is off keeps
 * the resting state free of an extra view.
 */
export function Spinning({ enabled, children }: { enabled: boolean; children: ReactNode }) {
	const spin = useRef(new Animated.Value(0)).current;
	const reduceMotion = useReducedMotion();
	const animate = shouldSpin(reduceMotion, enabled);
	useEffect(() => {
		if (!animate) return;
		const loop = Animated.loop(
			Animated.timing(spin, {
				toValue: 1,
				duration: SPIN_MS,
				easing: Easing.linear,
				useNativeDriver: true,
			}),
		);
		loop.start();
		return () => {
			loop.stop();
			// Back to the starting angle: a stopped loop otherwise leaves the glyph
			// frozen mid-turn, which reads as the progress it is depicting.
			spin.setValue(0);
		};
	}, [animate, spin]);
	if (!animate) return <>{children}</>;
	return (
		<Animated.View
			style={{ transform: [{ rotate: spin.interpolate({ inputRange: [0, 1], outputRange: ["0deg", "360deg"] }) }] }}
		>
			{children}
		</Animated.View>
	);
}

// A selectable pill - used by the project switcher, PR filters, and spawn picker
// so the active/inactive color logic lives in exactly one place.
export function Pill({
	label,
	active,
	onPress,
	style,
	textStyle,
}: {
	label: string;
	active: boolean;
	onPress: () => void;
	style?: StyleProp<ViewStyle>;
	textStyle?: StyleProp<TextStyle>;
}) {
	const s = useThemedStyles(makeStyles);
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityState={{ selected: active }}
			onPress={() => {
				haptics.select();
				onPress();
			}}
			style={[s.pill, active && s.pillActive, style]}
		>
			<Text
				numberOfLines={1}
				maxFontSizeMultiplier={fontScaleCap.chrome}
				style={[s.pillText, active && s.pillTextActive, textStyle]}
			>
				{label}
			</Text>
		</Pressable>
	);
}

export function StatusBadge({ status }: { status?: string | null }) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const v = statusVisual(t, status);
	return (
		<View style={s.badge}>
			<Dot color={v.color} breathing={v.breathing} size={8} />
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={[s.badgeText, { color: v.color }]}>
				{v.label}
			</Text>
		</View>
	);
}

export function Chip({
	label,
	color,
	tint,
	mono = false,
	icon,
}: {
	label: string;
	color?: string;
	tint?: string;
	mono?: boolean;
	icon?: keyof typeof Feather.glyphMap;
}) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	// Resolved here rather than as default parameter values: a default reads the
	// palette at module load, which would pin every unstyled chip to dark.
	const fg = color ?? t.textSecondary;
	const bg = tint ?? t.bgSubtle;
	return (
		<View style={[s.chip, { backgroundColor: bg }]}>
			{icon ? <Feather name={icon} size={12} color={fg} style={{ marginRight: space.xxs }} /> : null}
			<Text
				style={[s.chipText, { color: fg }, mono && { fontFamily: t.fontMono, fontSize: type.caption2.fontSize }]}
				numberOfLines={1}
				maxFontSizeMultiplier={fontScaleCap.chrome}
			>
				{label}
			</Text>
		</View>
	);
}
