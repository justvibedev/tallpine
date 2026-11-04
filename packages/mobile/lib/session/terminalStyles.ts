import { StyleSheet } from "react-native";

import type { Theme } from "../theme";

import { space, type } from "../tokens";

export const makeStyles = (t: Theme) =>
	StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: {
		flex: 1,
		alignItems: "center",
		justifyContent: "center",
		backgroundColor: t.bgBase,
	},
	statusBar: {
		flexDirection: "row",
		alignItems: "center",
		paddingHorizontal: space.md,
		paddingVertical: space.xs,
		borderBottomWidth: 1,
		borderBottomColor: t.borderSubtle,
	},
	statusDot: { width: 8, height: 8, borderRadius: 4, borderCurve: "continuous", marginRight: space.sm },
	statusText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.caption1.fontSize, flex: 1 },
	dims: { color: t.textTertiary, fontSize: type.caption2.fontSize, fontFamily: t.fontMono },
	zoomGroup: {
		flexDirection: "row",
		alignItems: "center",
		marginLeft: space.sm,
		borderWidth: 1,
		borderColor: t.borderDefault,
		borderRadius: 8, borderCurve: "continuous",
		backgroundColor: t.bgElevated,
		overflow: "hidden",
	},
	zoomBtn: { width: 28, height: 24, alignItems: "center", justifyContent: "center" },
	zoomDivider: { width: 1, height: 24, backgroundColor: t.borderDefault },
	banner: {
		backgroundColor: t.bgElevated,
		paddingHorizontal: space.md,
		paddingVertical: space.sm,
		borderBottomWidth: 1,
		borderBottomColor: t.borderDefault,
	},
	bannerText: { fontFamily: "Geist_400Regular", color: t.amber, fontSize: type.caption1.fontSize },
	interfaceFailureActions: {
		marginTop: space.sm,
		flexDirection: "row",
		alignItems: "center",
		justifyContent: "flex-end",
		gap: space.lg,
	},
	interfaceFailureActionText: { fontFamily: "Geist_600SemiBold", color: t.red, fontSize: type.caption1.fontSize, fontWeight: "600" },
	interfaceFailureDismissText: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.caption1.fontSize, fontWeight: "600" },
	termWrap: { flex: 1, backgroundColor: t.bgBase },
	dock: {
		borderTopWidth: 1,
		borderTopColor: t.borderSubtle,
		backgroundColor: t.bgSurface,
	},
	// Square-ish now that it holds a glyph and no label.
	killBtn: {
		alignItems: "center",
		justifyContent: "center",
		backgroundColor: t.tintRed,
		borderRadius: 12, borderCurve: "continuous",
		paddingHorizontal: space.sm,
		paddingVertical: space.xxs,
		marginLeft: space.md,
	},
	// Bare glyph in the nav bar: iOS draws its own round box behind header buttons,
	// so a tinted pill here would fight it. Colour carries the state instead.
	// A fixed square with centred content, not padding — asymmetric padding leaves
	// the glyph visibly off-centre inside the circle iOS draws around it.
	headerBrowserBtn: {
		width: 35,
		height: 30,
		alignItems: "center",
		justifyContent: "center",
	},
	headerActions: { flexDirection: "row", alignItems: "center" },
	interfaceOverlay: {
		...StyleSheet.absoluteFill,
		alignItems: "center",
		justifyContent: "center",
		paddingHorizontal: space.xxl,
		backgroundColor: t.scrim,
	},
	interfaceCard: {
		width: "100%",
		maxWidth: 340,
		alignItems: "center",
		gap: space.sm,
		paddingHorizontal: space.xl,
		paddingVertical: space.xxl,
		borderRadius: 16, borderCurve: "continuous",
		borderWidth: 1,
		borderColor: t.borderDefault,
		backgroundColor: t.bgSurface,
	},
	interfaceTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.callout.fontSize, fontWeight: "600" },
	interfaceCopy: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, textAlign: "center" },
	interfaceCancel: {
		marginTop: space.xxs,
		borderRadius: 8, borderCurve: "continuous",
		borderWidth: 1,
		borderColor: t.borderDefault,
		paddingHorizontal: space.md,
		paddingVertical: space.sm,
	},
	interfaceCancelText: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.caption1.fontSize, fontWeight: "600" },
	interfaceError: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, textAlign: "center" },
	// Small green badge on the globe when a real preview is available. Offsets are
	// from the square's centre, so it rides the glyph rather than the button frame.
	browserReadyDot: {
		position: "absolute",
		top: 4,
		right: 3,
		width: 8,
		height: 8,
		borderRadius: 4, borderCurve: "continuous",
		backgroundColor: t.green,
		borderWidth: 1,
		borderColor: t.bgSurface,
	},
	restoreBtn: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.xxs,
		backgroundColor: t.accentTint,
		borderRadius: 12, borderCurve: "continuous",
		paddingHorizontal: space.md,
		paddingVertical: space.xxs,
		marginLeft: space.md,
	},
	restoreText: { fontFamily: "Geist_600SemiBold", color: t.accent, fontWeight: "600", fontSize: type.caption1.fontSize },
	deadOverlay: {
		...StyleSheet.absoluteFill,
		alignItems: "center",
		justifyContent: "center",
		padding: space.xxxl,
		gap: space.sm,
		backgroundColor: t.bgBase,
	},
	deadIcon: {
		width: 64,
		height: 64,
		borderRadius: 16, borderCurve: "continuous",
		backgroundColor: t.bgElevated,
		borderWidth: 1,
		borderColor: t.borderSubtle,
		alignItems: "center",
		justifyContent: "center",
		marginBottom: space.xs,
	},
	deadTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.body.fontSize, fontWeight: "600", textAlign: "center" },
	deadMsg: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, textAlign: "center", maxWidth: 300 },
	restoreCta: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		backgroundColor: t.accent,
		borderRadius: 8, borderCurve: "continuous",
		paddingVertical: space.md,
		paddingHorizontal: space.xl,
		marginTop: space.sm,
	},
	restoreCtaText: { fontFamily: "Geist_600SemiBold", color: t.onAccent, fontSize: type.subheadline.fontSize, fontWeight: "600" },
	// Accent-blue like the rest of the chrome. Red is reserved for the mic button
	// alone: one small saturated element reads as "recording", a whole red panel
	// reads as an error. It sits at the top of the dock, so it divides itself from
	// the keys below rather than redrawing the dock's own top border.
	voiceStrip: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		paddingHorizontal: space.md,
		paddingVertical: space.xs,
		backgroundColor: t.accentTint,
		borderBottomWidth: 1,
		borderBottomColor: t.accent,
	},
	// Muted while the mic warms up, so "ready to speak" is a visible state change
	// and not just a wording difference.
	voiceStripWarmup: { backgroundColor: t.bgElevated, borderBottomColor: t.borderDefault },
	voiceText: { fontFamily: "Geist_400Regular", flex: 1, color: t.textPrimary, fontSize: type.footnote.fontSize },
});
