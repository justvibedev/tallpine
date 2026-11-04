import { StyleSheet } from "react-native";
import type { Theme } from "../theme";
import { iconSize, space, type } from "../tokens";
import { CORNER, CORNER_W } from "./pairConstants";

export const makeStyles = (t: Theme) =>
	StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	topBar: { paddingHorizontal: space.lg, paddingTop: space.xxs, paddingBottom: space.sm },

	steps: { paddingHorizontal: space.xl, paddingBottom: space.lg },
	repairIntro: { paddingHorizontal: space.xl, paddingBottom: space.lg },
	repairTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title3.fontSize, fontWeight: "600" },
	repairText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginTop: space.xs },
	missing: { paddingHorizontal: space.xl, gap: space.md },
	missingTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title3.fontSize, fontWeight: "600", textAlign: "center" },
	missingText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, textAlign: "center" },

	viewfinder: {
		flex: 1,
		marginHorizontal: space.lg,
		borderRadius: 16, borderCurve: "continuous",
		overflow: "hidden",
		// The viewfinder is a camera preview, so it stays dark in both themes.
		backgroundColor: "#0c0d10",
	},
	corner: {
		position: "absolute",
		width: CORNER,
		height: CORNER,
		borderColor: "rgba(255,255,255,0.92)",
	},
	cTL: { top: 14, left: 14, borderTopWidth: CORNER_W, borderLeftWidth: CORNER_W, borderTopLeftRadius: 6 },
	cTR: { top: 14, right: 14, borderTopWidth: CORNER_W, borderRightWidth: CORNER_W, borderTopRightRadius: 6 },
	cBL: { bottom: 14, left: 14, borderBottomWidth: CORNER_W, borderLeftWidth: CORNER_W, borderBottomLeftRadius: 6 },
	cBR: { bottom: 14, right: 14, borderBottomWidth: CORNER_W, borderRightWidth: CORNER_W, borderBottomRightRadius: 6 },

	gate: { flex: 1, alignItems: "center", justifyContent: "center", padding: space.xxl },
	gateTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.callout.fontSize, fontWeight: "600", marginTop: space.md },
	gateHint: { fontFamily: "Geist_400Regular",
		color: t.textSecondary,
		fontSize: type.footnote.fontSize,
		lineHeight: type.footnote.lineHeight,
		textAlign: "center",
		marginTop: space.sm,
		maxWidth: 300,
	},

	// Connecting and failure share one quiet slot under the viewfinder: no fill
	// or border, aligned with the numbered steps above.
	notice: {
		flexDirection: "row",
		gap: space.sm,
		alignItems: "flex-start",
		paddingHorizontal: space.xl,
		marginTop: space.lg,
	},
	noticeLead: { width: iconSize.sm, height: type.subheadline.lineHeight, alignItems: "center", justifyContent: "center" },
	noticeBody: { flex: 1, gap: space.xxs },
	noticeTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
	noticeText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	noticeActions: { flexDirection: "row", flexWrap: "wrap", gap: space.lg, marginTop: space.sm },
	noticeAction: { flexDirection: "row", alignItems: "center", gap: space.xs },
	noticeActionText: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.footnote.fontSize, fontWeight: "600" },

	manual: {
		flexDirection: "row",
		alignItems: "center",
		justifyContent: "center",
		gap: space.sm,
		paddingTop: space.lg,
	},
	manualText: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.subheadline.fontSize, fontWeight: "600" },
});

