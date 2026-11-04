import { StyleSheet } from "react-native";
import type { Theme } from "../theme";
import { space, type } from "../tokens";
import { composerStyleRules } from "./composerStyles";

export const makeStyles = (t: Theme) => StyleSheet.create({
	...composerStyleRules(t),
	reviewArea: { width: "100%", gap: space.xs },
	reviewAreaWithPrompt: { gap: 0 },
	reviewDivider: { width: "100%", borderTopWidth: 1, borderTopColor: t.borderStrong },
	reviewContainer: { width: "100%", paddingHorizontal: space.sm, paddingVertical: space.xs, backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.accentBorder, borderRadius: 24, borderCurve: "continuous", overflow: "hidden" },
	reviewCardHeader: { overflow: "hidden" },
	// Three things can share this row — the turn settings, the queued-message note
	// and the context meter — and the settings label is the only one that can be
	// long. It is the one that yields: `flex: 1` with `minWidth: 0` to allow the
	// squeeze, and `overflow: "hidden"` so a label that does not truncate cleanly
	// cannot paint over its neighbours. The `gap` is the floor under that: even
	// when everything fits, the two never touch.
	metaRow: { width: "100%", height: 44, flexDirection: "row", alignItems: "center", gap: space.sm },
	settingsSlot: { flex: 1, minWidth: 0, height: 44, alignItems: "flex-start", justifyContent: "center", overflow: "hidden" },
	quotes: { maxHeight: 120, flexGrow: 0, marginTop: space.xxs, marginHorizontal: space.sm },
	quote: { minHeight: 44, flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: space.sm },
	quoteText: { ...type.subheadline, flex: 1, minWidth: 0, paddingVertical: space.xs, color: t.textSecondary },
	removeQuote: { width: 44, height: 44, alignItems: "center", justifyContent: "center" },
	reviewComposer: { backgroundColor: "transparent" },
	// The way back to a request the user pushed aside to type instead.
	restore: { flexDirection: "row", alignItems: "center", gap: space.xs, paddingVertical: space.xs, paddingHorizontal: space.sm, borderRadius: 12, backgroundColor: t.tintAmber },
	restoreText: { fontFamily: "Geist_600SemiBold", flex: 1, minWidth: 0, color: t.amber, fontSize: type.caption1.fontSize, fontWeight: "600" },
	restoreAction: { fontFamily: "Geist_600SemiBold", color: t.amber, fontSize: type.caption1.fontSize, fontWeight: "600", textDecorationLine: "underline" },
	// Sits at the end of the meta row; the bar carries the reading, the label names it.
	contextMeter: { flexShrink: 0, flexDirection: "row", alignItems: "center", gap: space.xs, height: 32, paddingLeft: space.sm },
	contextTrack: { width: 34, height: 4, borderRadius: 2, overflow: "hidden", backgroundColor: t.bgSubtle },
	contextFill: { height: 4, borderRadius: 2 },
	contextText: { fontFamily: "Geist_600SemiBold", fontSize: type.caption2.fontSize, fontWeight: "600" },
	deliveryNote: { flexShrink: 0, maxWidth: "46%", height: 32, flexDirection: "row", alignItems: "center", justifyContent: "flex-end", gap: space.xxs, paddingRight: space.xxs },
	deliveryNoteText: { fontFamily: "Geist_600SemiBold", flexShrink: 1, color: t.textTertiary, fontSize: type.caption2.fontSize, fontWeight: "600" },
	changesPill: { minHeight: 30, flexShrink: 1, flexDirection: "row", alignItems: "center", gap: space.xxs, paddingHorizontal: space.sm, borderRadius: 15, borderCurve: "continuous", backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault },
	changesPillText: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.caption2.fontSize, fontWeight: "600" },
	changesPillCount: { fontFamily: t.fontMono, fontSize: type.caption2.fontSize },
	steerAction: { width: 30, height: 30, alignItems: "center", justifyContent: "center", borderRadius: 16, backgroundColor: t.accentTint },
	queueDock: { overflow: "hidden", backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault, borderRadius: 16, borderCurve: "continuous" },
	queueRow: { minHeight: 44, flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: space.md, paddingRight: space.xxs },
	queueRowDivider: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
	queueText: { fontFamily: "Geist_500Medium", flex: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
	queueSteer: { width: 40, height: 40, alignItems: "center", justifyContent: "center", borderRadius: 20 },
	queueDelete: { width: 40, height: 40, borderRadius: 20, alignItems: "center", justifyContent: "center" },
});

export function changesAccessibilityLabel(changes: { files: number; additions: number; deletions: number }): string {
	const files = `${changes.files} ${changes.files === 1 ? "file" : "files"} changed`;
	return `${files}, ${changes.additions} ${changes.additions === 1 ? "line" : "lines"} added, ${changes.deletions} removed`;
}
