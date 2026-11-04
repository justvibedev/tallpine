import { StyleSheet } from "react-native";
import { space, type } from "../tokens";
import { type Theme } from "../theme";



export const makeStyles = (t: Theme) =>
	StyleSheet.create({
		listSectionHeader: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.sm,
			paddingHorizontal: space.lg,
			paddingTop: space.lg,
			paddingBottom: space.xxs,
		},
		listSectionLabel: { fontFamily: "Geist_500Medium", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "500" },
		// Mono and tabular so a count changing from 9 to 10 does not shift the rule.
		listSectionCount: { color: t.textFaint, fontSize: type.caption1.fontSize, fontWeight: "600", fontFamily: t.fontMono },
		badge: { flexDirection: "row", alignItems: "center", gap: space.xs },
		badgeText: { fontFamily: "Geist_600SemiBold", fontSize: type.caption1.fontSize, fontWeight: "600" },

		pill: {
			paddingHorizontal: space.md,
			paddingVertical: space.xs,
			borderRadius: 20,
			borderWidth: 1,
			borderColor: t.borderDefault,
			backgroundColor: t.bgElevated,
		},
		pillActive: { backgroundColor: t.accentTint, borderColor: t.accent },
		pillText: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		pillTextActive: { color: t.accent },

		chip: {
			flexDirection: "row",
			alignItems: "center",
			paddingHorizontal: space.sm,
			paddingVertical: space.hair,
			borderRadius: 4,
		},
		chipText: { fontFamily: "Geist_600SemiBold", fontSize: type.caption2.fontSize, fontWeight: "600" },

		card: {
			backgroundColor: t.bgElevated,
			borderRadius: 12,
			borderWidth: 1,
			borderColor: t.borderSubtle,
			padding: space.md,
		},
		cardPressed: {
			backgroundColor: t.bgElevatedHover,
			borderColor: t.borderDefault,
		},

		sectionHeader: {
			flexDirection: "row",
			alignItems: "center",
			paddingHorizontal: space.lg,
			paddingTop: space.xl,
			paddingBottom: space.sm,
			gap: space.sm,
		},
		sectionBar: { width: 3, height: 13, borderRadius: 2 },
		sectionLabel: { fontFamily: "Geist_600SemiBold",
			color: t.textSecondary,
			fontSize: type.caption2.fontSize,
			letterSpacing: 1.2,
			fontWeight: "600",
			flex: 1,
		},
		sectionCount: {
			color: t.textTertiary,
			fontSize: type.caption1.fontSize,
			fontWeight: "600",
			fontFamily: t.fontMono,
		},

		screenHeader: {
			flexDirection: "row",
			alignItems: "center",
			paddingHorizontal: space.lg,
			paddingTop: space.sm,
			paddingBottom: space.sm,
			gap: space.md,
		},
		titleRow: { flexDirection: "row", alignItems: "center", gap: space.sm },
		// Enlarged from 30x26: the wand-tip lamp replaced the "live" pill, so the tip
		// has to be big enough for the dot to actually read as a status light.
		mascotWrap: { marginTop: space.hair },
		// Positioned on the wand tip by MascotLamp, which centres them on it.
		// zIndex, not paint order: Android orders siblings by elevation/zIndex, and
		// the halo used to carry `elevation` — which put a 28%-opacity circle *over*
		// the dot and washed it out. Stating both keeps the dot on top everywhere.
		wandTip: { position: "absolute", zIndex: 1 },
		wandHalo: {
			position: "absolute",
			opacity: 0.28,
			zIndex: 0,
			// iOS-only; Android has no glow-style shadow, so there the halo circle
			// alone stands in for it. No `elevation` — that draws a drop shadow
			// beneath the view and reorders it, neither of which is a glow.
			shadowOpacity: 0.9,
			shadowRadius: 6,
			shadowOffset: { width: 0, height: 0 },
		},
		screenTitle: { fontFamily: "Geist_600SemiBold",
			color: t.textPrimary,
			fontSize: type.title1.fontSize,
			fontWeight: "600",
			letterSpacing: -0.5,
		},
		screenSubtitle: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption1.fontSize, marginTop: space.none },
		headerIconBtn: {
			alignItems: "center",
			justifyContent: "center",
			padding: space.hair,
		},
		headerBadge: {
			position: "absolute",
			top: 0,
			right: 1,
			width: 9,
			height: 9,
			borderRadius: 4,
			backgroundColor: t.accent,
			borderWidth: 1.5,
			borderColor: t.bgBase,
		},

		btn: {
			borderRadius: 8,
			paddingVertical: space.md,
			paddingHorizontal: space.lg,
			alignItems: "center",
		},
		btnInner: { flexDirection: "row", alignItems: "center" },
		btnPrimary: { backgroundColor: t.accent },
		btnGhost: {
			borderWidth: 1,
			borderColor: t.borderStrong,
			backgroundColor: t.bgElevated,
		},
		btnDanger: { borderColor: t.tintRed, backgroundColor: t.tintRed },
		btnSuccess: { borderColor: t.green, backgroundColor: t.green },
		btnText: { fontFamily: "Geist_600SemiBold", fontSize: type.subheadline.fontSize, fontWeight: "600" },

		step: {
			flexDirection: "row",
			alignItems: "flex-start",
			gap: space.md,
			paddingVertical: space.md,
		},
		stepCompact: { paddingVertical: space.xs, alignItems: "center", gap: space.md },
		stepBadge: {
			width: 30,
			height: 30,
			borderRadius: 8,
			backgroundColor: t.bgElevated,
			borderWidth: 1,
			borderColor: t.borderSubtle,
			alignItems: "center",
			justifyContent: "center",
		},
		stepBadgeCompact: { width: 23, height: 23, borderRadius: 12 },
		stepNum: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		stepNumCompact: { fontFamily: "Geist_400Regular", fontSize: type.caption2.fontSize },
		stepTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.subheadline.fontSize, fontWeight: "600" },
		stepTitleCompact: { fontFamily: "Geist_600SemiBold", fontSize: type.subheadline.fontSize, fontWeight: "600" },
		stepHint: { fontFamily: "Geist_400Regular",
			color: t.textTertiary,
			fontSize: type.footnote.fontSize,
			lineHeight: type.footnote.lineHeight,
			marginTop: space.hair,
		},

		// No card, corners or grabber here — the native sheet draws all of that. The
		// top padding leaves room for the OS grabber so the title doesn't sit under it.
		// No bottom safe-area inset on either of these: the native sheet already
		// reserves room for the home indicator, and adding it again left a dead strip.
		sheetScreen: {
			backgroundColor: t.bgSurface,
			paddingHorizontal: space.xl,
			paddingTop: space.xl,
			paddingBottom: space.xl,
		},
		// Padding lives on the scroll content rather than a wrapper, so a scrolling
		// sheet's list can run edge to edge while its rows keep the same inset.
		sheetHeader: { paddingBottom: space.hair },
		sheetTitleRow: { flexDirection: "row", alignItems: "center", gap: space.md },
		sheetTitle: { fontFamily: "Geist_600SemiBold",
			color: t.textPrimary,
			fontSize: type.title3.fontSize,
			fontWeight: "600",
			letterSpacing: -0.3,
		},
		sheetSubtitle: { fontFamily: "Geist_400Regular",
			color: t.textSecondary,
			fontSize: type.footnote.fontSize,
			lineHeight: type.footnote.lineHeight,
			marginTop: space.xxs,
		},

		group: { marginBottom: space.xxl },
		groupTitle: { fontFamily: "Geist_600SemiBold",
			color: t.textTertiary,
			fontSize: type.caption2.fontSize,
			letterSpacing: 1.2,
			fontWeight: "600",
			marginBottom: space.sm,
			marginLeft: space.xxs,
		},
		groupBody: {
			backgroundColor: t.bgElevated,
			borderRadius: 12,
			borderWidth: 1,
			borderColor: t.borderSubtle,
			overflow: "hidden",
		},
		groupFooter: { fontFamily: "Geist_400Regular",
			color: t.textTertiary,
			fontSize: type.caption1.fontSize,
			lineHeight: type.caption1.lineHeight,
			marginTop: space.sm,
			marginHorizontal: space.xxs,
		},
		// Inset to the label's x-origin (row padding + icon + gap), the iOS detail
		// that makes a stack of rows read as one grouped list.
		separator: {
			height: StyleSheet.hairlineWidth,
			backgroundColor: t.borderDefault,
			marginLeft: 43,
		},
		row: {
			flexDirection: "row",
			alignItems: "center",
			minHeight: 48,
			paddingVertical: space.md,
			paddingHorizontal: space.md,
			gap: space.sm,
		},
		rowPressed: { backgroundColor: t.bgElevatedHover },
		rowDisabled: { opacity: 0.45 },
		rowIcon: { width: 17, marginRight: space.xxs },
		rowLabel: { fontFamily: "Geist_500Medium",
			flex: 1,
			minWidth: 0,
			color: t.textPrimary,
			fontSize: type.subheadline.fontSize,
			fontWeight: "500",
		},
		rowValue: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.subheadline.fontSize, flexShrink: 1 },
		rowChevron: { marginRight: -3 },
		rowAccessory: { flexShrink: 0, flexDirection: "row", alignItems: "center" },

		iconBtn: {
			width: 32,
			height: 32,
			borderRadius: 8,
			borderWidth: 1,
			borderColor: t.borderDefault,
			backgroundColor: t.bgSubtle,
			alignItems: "center",
			justifyContent: "center",
		},
		iconBtnPressed: { backgroundColor: t.accentTint, borderColor: t.accent },
		iconBtnPressedDanger: { backgroundColor: t.tintRed, borderColor: t.red },

		empty: {
			flex: 1,
			alignItems: "center",
			justifyContent: "center",
			padding: space.huge,
			minHeight: 320,
		},
		emptyIcon: {
			width: 64,
			height: 64,
			borderRadius: 16,
			backgroundColor: t.bgElevated,
			borderWidth: 1,
			borderColor: t.borderSubtle,
			alignItems: "center",
			justifyContent: "center",
			marginBottom: space.lg,
		},
		emptyTitle: { fontFamily: "Geist_600SemiBold",
			color: t.textPrimary,
			fontSize: type.body.fontSize,
			fontWeight: "600",
			textAlign: "center",
		},
		emptyMsg: { fontFamily: "Geist_400Regular",
			color: t.textSecondary,
			fontSize: type.footnote.fontSize,
			lineHeight: type.footnote.lineHeight,
			textAlign: "center",
			marginTop: space.sm,
			maxWidth: 300,
		},
	});
