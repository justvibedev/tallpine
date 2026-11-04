import { Feather } from "../icons";
import { type ReactNode } from "react";
import { Animated, Text, View } from "react-native";
import { useEnterTransition } from "../motionHooks";
import { fontScaleCap, space } from "../tokens";
import { useTheme, useThemedStyles } from "../ThemeProvider";


import { useBreathing } from "./indicators";
import { makeStyles } from "./styles";

export function EmptyState({
	icon = "inbox",
	title,
	message,
	action,
	pulse = false,
}: {
	icon?: keyof typeof Feather.glyphMap;
	title: string;
	message?: string;
	action?: ReactNode;
	/** Breathe the icon, for a state that is waiting on something. */
	pulse?: boolean;
}) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const breathing = useBreathing(pulse);
	// An empty state appears once per screen and then sits still, so it can afford
	// a quiet entrance. Lists and rows stay unstaggered.
	const enter = useEnterTransition(space.xxs);
	return (
		<Animated.View style={[s.empty, enter]}>
			<View style={s.emptyIcon}>
				<Animated.View style={breathing ? { opacity: breathing } : undefined}>
					<Feather name={icon} size={24} color={t.textTertiary} />
				</Animated.View>
			</View>
			<Text maxFontSizeMultiplier={fontScaleCap.body} style={s.emptyTitle}>
				{title}
			</Text>
			{message ? (
				<Text maxFontSizeMultiplier={fontScaleCap.body} style={s.emptyMsg}>
					{message}
				</Text>
			) : null}
			{action ? <View style={{ marginTop: space.lg }}>{action}</View> : null}
		</Animated.View>
	);
}
