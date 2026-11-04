import { StyleSheet } from "react-native";
import type { Theme } from "../theme";
import { type, space } from "../tokens";

export const makeStyles = (t: Theme) => StyleSheet.create({
	composerHidden: { display: "none" },
	screen: { flex: 1, backgroundColor: t.bgBase },
	banner: { minHeight: 35, flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: space.md, paddingRight: space.sm, paddingVertical: space.xs, borderBottomWidth: 1, borderBottomColor: t.borderSubtle },
	bannerIcon: { alignSelf: "flex-start", marginTop: space.hair },
	bannerCopy: { flex: 1, minWidth: 0, gap: space.none },
	bannerTitle: { fontFamily: "Geist_600SemiBold", fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
	bannerText: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	bannerClose: { width: 26, height: 26, alignItems: "center", justifyContent: "center" },
	bannerAction: { fontFamily: "Geist_600SemiBold", fontSize: type.caption2.fontSize, fontWeight: "600" },
	bannerSecondary: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.caption2.fontSize, fontWeight: "600" },
	reviewHeaderPRButton: { width: 44, height: 44, borderRadius: 22, borderCurve: "continuous", borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderStrong, alignItems: "center", justifyContent: "center", overflow: "hidden" },
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, paddingHorizontal: space.huge, backgroundColor: t.bgBase },
	// Where the timeline will draw the checklist, so the handoff to it does not move it.
	startupLoading: { flex: 1, backgroundColor: t.bgBase, paddingHorizontal: space.xl, paddingTop: space.md },
	centerTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.body.fontSize, fontWeight: "600", textAlign: "center" },
	centerCopy: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, textAlign: "center" },
	centerAction: { minHeight: 42, justifyContent: "center", backgroundColor: t.accent, borderRadius: 12, paddingHorizontal: space.lg, marginTop: space.xxs },
	centerActionText: { fontFamily: "Geist_600SemiBold", color: t.onAccent, fontSize: type.footnote.fontSize, fontWeight: "600" },
});
