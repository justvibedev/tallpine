import { useCallback, useEffect, useMemo, useRef } from "react";
import { PanResponder, StyleSheet } from "react-native";
import { Easing, interpolateColor, runOnJS, useAnimatedStyle, useSharedValue, withSpring, withTiming } from "react-native-reanimated";
import { useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import { haptics } from "../haptics";
import type { Theme } from "../theme";
import { space } from "../tokens";
import { KEYBOARD_DOCK_GAP } from "../session/keyboardInset";
import { composerGlassSupported } from "./composer-glass";
import { COMPOSER_RADIUS } from "./composerStyles";
import type { DashboardPR } from "../api";

export function useReviewCardMotion({
	reviewPR,
	onOpenReview,
	reviewPRCollapsed,
	onCollapseReviewPR,
	t,
	restingInset,
	dockRiseOverride,
	keyboard,
}: {
	reviewPR: DashboardPR | undefined;
	onOpenReview: (() => void) | undefined;
	reviewPRCollapsed: boolean | undefined;
	onCollapseReviewPR: (() => void) | undefined;
	t: Theme;
	restingInset: number;
	dockRiseOverride: number | undefined;
	keyboard: ReturnType<typeof useReanimatedKeyboardAnimation>;
}) {
	const reviewPromptAvailable = Boolean(reviewPR && onOpenReview);
	const reviewCardDragY = useSharedValue(0);
	const reviewCardHeaderHeight = useSharedValue(0);
	const reviewCardHeaderMeasured = useSharedValue(false);
	const wasReviewPromptCollapsed = useRef(Boolean(reviewPRCollapsed));
	const reviewCardHeaderStyle = useAnimatedStyle(() => {
		if (!reviewCardHeaderMeasured.value) return {};
		const progress = Math.max(0, Math.min(reviewCardDragY.value / 180, 1));
		return {
			height: reviewCardHeaderHeight.value * (1 - progress),
			opacity: 1 - progress,
			transform: [{ translateY: -12 * progress }],
		};
	});
	const finishReviewCardCollapse = useCallback(() => { onCollapseReviewPR?.(); }, [onCollapseReviewPR]);
	const collapseReviewCard = useCallback(() => {
		haptics.tap();
		reviewCardDragY.value = withTiming(180, { duration: 150 }, (finished) => {
			if (finished) runOnJS(finishReviewCardCollapse)();
		});
	}, [finishReviewCardCollapse, reviewCardDragY]);
	const reviewCardPan = useMemo(() => PanResponder.create({
		onMoveShouldSetPanResponderCapture: (_event, gesture) => gesture.dy > 5 && gesture.dy > Math.abs(gesture.dx),
		onPanResponderTerminationRequest: () => false,
		onPanResponderGrant: () => { reviewCardDragY.value = 0; },
		onPanResponderMove: (_event, gesture) => { reviewCardDragY.value = Math.max(0, gesture.dy); },
		onPanResponderRelease: (_event, gesture) => {
			if (gesture.dy > 72) collapseReviewCard();
			else reviewCardDragY.value = withSpring(0, { damping: 18, stiffness: 220 });
		},
		onPanResponderTerminate: () => {
			if (reviewCardDragY.value > 72) collapseReviewCard();
			else reviewCardDragY.value = withSpring(0, { damping: 18, stiffness: 220 });
		},
	}), [collapseReviewCard, reviewCardDragY]);
	const reviewCardDragStyle = useAnimatedStyle(() => {
		const progress = Math.max(0, Math.min(reviewCardDragY.value / 180, 1));
		const expandedSurface = composerGlassSupported ? "transparent" : t.bgElevated;
		return {
			// Keep only the composer pill elevated while dragging. If this shell stays
			// elevated too, it reads as a gray slab behind the composer until release.
			backgroundColor: interpolateColor(progress, [0, 1], [expandedSurface, t.bgBase]),
			borderColor: interpolateColor(progress, [0, 1], [t.accentBorder, t.bgBase]),
			borderWidth: StyleSheet.hairlineWidth * (1 - progress),
			borderRadius: 24 + (COMPOSER_RADIUS - 24) * progress,
			paddingHorizontal: space.sm * (1 - progress),
			paddingVertical: space.xs * (1 - progress),
		};
	});
	const reviewCardGlassStyle = useAnimatedStyle(() => ({ opacity: 1 - Math.max(0, Math.min(reviewCardDragY.value / 180, 1)) }));
	const reviewComposerSurfaceStyle = useAnimatedStyle(() => ({
		backgroundColor: interpolateColor(Math.max(0, Math.min(reviewCardDragY.value / 180, 1)), [0, 1], ["transparent", t.bgElevated]),
	}));
	const reviewComposerGlassStyle = useAnimatedStyle(() => ({ opacity: Math.max(0, Math.min(reviewCardDragY.value / 180, 1)) }));
	useEffect(() => {
		if (reviewPRCollapsed) {
			reviewCardDragY.value = 180;
		} else if (wasReviewPromptCollapsed.current && reviewPromptAvailable) {
			// Glide the card up from the composer shape without a spring or bounce.
			reviewCardDragY.value = withTiming(0, { duration: 240, easing: Easing.out(Easing.cubic) });
		}
		wasReviewPromptCollapsed.current = Boolean(reviewPRCollapsed);
	}, [reviewCardDragY, reviewPRCollapsed, reviewPromptAvailable]);
	const dockRise = useAnimatedStyle(() => ({
		transform: [{ translateY: (restingInset - KEYBOARD_DOCK_GAP) * (dockRiseOverride ?? keyboard.progress.value) }],
	}));

	return { reviewPromptAvailable, reviewCardHeaderHeight, reviewCardHeaderMeasured, reviewCardHeaderStyle, collapseReviewCard, reviewCardPan, reviewCardDragStyle, reviewCardGlassStyle, reviewComposerSurfaceStyle, reviewComposerGlassStyle, dockRise };
}
