import { Feather } from "../icons";
import { type ReactNode } from "react";
import { Animated, Pressable, Text, View, type StyleProp, type ViewStyle } from "react-native";
import { haptics } from "../haptics";
import { usePressScale } from "../motionHooks";
import { NativeHeaderButton, type NativeHeaderButtonIcon } from "../native-header-button";
import { useOptionalSidebarNavigation } from "../sidebar-navigation-context";
import { fontScaleCap, space } from "../tokens";
import { type Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";


import { makeStyles } from "./styles";

export function Card({
	children,
	onPress,
	style,
}: {
	children: ReactNode;
	onPress?: () => void;
	style?: StyleProp<ViewStyle>;
}) {
	const s = useThemedStyles(makeStyles);
	const pressFx = usePressScale();
	if (!onPress) return <View style={[s.card, style]}>{children}</View>;
	return (
		// The animated transform lives on a wrapper: `Animated.createAnimatedComponent`
		// drops function styles, and the card's pressed state needs one.
		<Animated.View style={[style, pressFx.style]}>
			<Pressable
				onPressIn={pressFx.onPressIn}
				onPressOut={pressFx.onPressOut}
				onPress={() => {
					haptics.tap();
					onPress();
				}}
				style={({ pressed }) => [s.card, pressed && s.cardPressed]}
			>
				{children}
			</Pressable>
		</Animated.View>
	);
}

export function SectionHeader({ label, color, count }: { label: string; color: string; count?: number }) {
	const s = useThemedStyles(makeStyles);
	return (
		<View style={s.sectionHeader}>
			<View style={[s.sectionBar, { backgroundColor: color }]} />
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={s.sectionLabel}>
				{label.toUpperCase()}
			</Text>
			{count !== undefined ? (
				<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={s.sectionCount}>
					{count}
				</Text>
			) : null}
		</View>
	);
}

// A bare glyph for the header's trailing slot. Deliberately not `IconButton`,
// which is a bordered card action and reads as a box when dropped into a header.
export function HeaderIconButton({
	icon,
	label,
	onPress,
	badge = 0,
}: {
	icon: NativeHeaderButtonIcon;
	/** Required — the control has no visible text. */
	label: string;
	onPress: () => void;
	/** Non-zero shows an unread dot. The number itself is not drawn. */
	badge?: number;
}) {
	const s = useThemedStyles(makeStyles);
	return (
		<View style={s.headerIconBtn} accessibilityLabel={badge > 0 ? `${label}, ${badge} unread` : label}>
			<NativeHeaderButton
				icon={icon}
				label={badge > 0 ? `${label}, ${badge} unread` : label}
				onPress={() => {
					haptics.tap();
					onPress();
				}}
			/>
			{badge > 0 ? <View style={s.headerBadge} /> : null}
		</View>
	);
}

export function ScreenHeader({
	title,
	left,
	right,
}: {
	title: string;
	/** Detail routes can supply a back action instead of the sidebar button. */
	left?: ReactNode;
	right?: ReactNode;
}) {
	const s = useThemedStyles(makeStyles);
	const sidebar = useOptionalSidebarNavigation();
	return (
		<View style={s.screenHeader}>
			{left ?? (sidebar ? <HeaderIconButton icon="menu" label="Open navigation" onPress={sidebar.openSidebar} /> : null)}
			<View style={{ flex: 1, minWidth: 0 }}>
				<Text maxFontSizeMultiplier={fontScaleCap.title} numberOfLines={1} ellipsizeMode="tail" style={s.screenTitle}>
					{title}
				</Text>
			</View>
			{right}
		</View>
	);
}

/**
 * A board group's heading. With `onToggle` it folds its section away — same
 * typography and rule as before, plus the chevron that says so.
 *
 * The chevron sits beside the label rather than at the far edge so the row still
 * reads as one heading with a rule running out of it, which is what the board
 * looked like before sections could be folded.
 */
export function ListSectionHeader({
	label,
	count,
	open = true,
	onToggle,
}: {
	label: string;
	count?: number;
	open?: boolean;
	onToggle?: () => void;
}) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const body = (
		<>
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={s.listSectionLabel}>
				{label}
			</Text>
			{onToggle ? (
				<Feather name={open ? "chevron-down" : "chevron-right"} size={15} color={t.textTertiary} />
			) : null}
			{count !== undefined ? (
				<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={s.listSectionCount}>
					{count}
				</Text>
			) : null}
		</>
	);

	if (!onToggle) return <View style={s.listSectionHeader}>{body}</View>;
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityState={{ expanded: open }}
			accessibilityLabel={`${label} section`}
			onPress={onToggle}
			style={({ pressed }) => [s.listSectionHeader, pressed && { opacity: 0.6 }]}
		>
			{body}
		</Pressable>
	);
}

/**
 * The shell every card in the app shares: session cards, PR cards, and the
 * orchestrator's project card.
 *
 * These three had byte-identical style blocks, each with a comment
 * acknowledging the duplication ("Matches the session card shell so a PR card and
 * a session card read as siblings"). Comments cannot keep them in step — a
 * radius changed in one place would quietly make one card a different shape
 * from its neighbours in the same scroll view. This is what those comments were
 * describing, made real.
 *
 * Spread it and add whatever a specific card needs on top:
 *   card: { ...cardShell(t), marginBottom: 0 }
 */
export function cardShell(t: Theme): ViewStyle {
	return {
		backgroundColor: t.bgElevated,
		borderRadius: 12,
		borderWidth: 1,
		borderColor: t.borderSubtle,
		paddingHorizontal: space.md,
		paddingVertical: space.md,
		marginHorizontal: space.md,
		marginVertical: space.xxs,
	};
}

/** The pressed state for a tappable cardShell. */
export function cardShellPressed(t: Theme): ViewStyle {
	return { backgroundColor: t.bgElevatedHover, borderColor: t.borderDefault };
}
