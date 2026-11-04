import { type ReactNode } from "react";
import { Text, View } from "react-native";
import { space } from "../tokens";
import { useThemedStyles } from "../ThemeProvider";


import { makeStyles } from "./styles";

// The body of a sheet. The sheet chrome itself — the card, the rounded corners,
// the grabber and the drag-to-dismiss — is now the OS's job: these render inside
// native `formSheet` routes (see `app/sheets/*` and the registrations in
// `app/_layout.tsx`). Re-implementing that in JS on top of RN's `Modal` never
// felt native, because `Modal` is a plain container with no gesture support.
/**
 * The title block of a sheet. Separate from `SheetScreen` because the scrolling
 * sheets pass it as their list's `ListHeaderComponent` rather than rendering it
 * as a sibling of the list: a scroller sitting next to static siblings inside a
 * sheet laid the two on top of each other. One scrolling root, header inside it,
 * has no such interplay — and it matches how iOS's own sheets behave.
 */
export function SheetHeader({
	title,
	subtitle,
	right,
}: {
	title: string;
	subtitle?: string;
	/** Trailing control on the title row — the agent sheet's Refresh lives here. */
	right?: ReactNode;
}) {
	const s = useThemedStyles(makeStyles);
	return (
		<View style={s.sheetHeader}>
			<View style={s.sheetTitleRow}>
				<Text style={[s.sheetTitle, { flex: 1 }]}>{title}</Text>
				{right}
			</View>
			{subtitle ? <Text style={s.sheetSubtitle}>{subtitle}</Text> : null}
		</View>
	);
}

/**
 * `contentContainerStyle` for a scrolling sheet — the inset its header and rows
 * share. Pure padding, so it needs no theme and can be a plain const.
 */
export const SHEET_SCROLL_CONTENT = {
	paddingHorizontal: space.xl,
	paddingTop: space.xl,
	paddingBottom: space.xxl,
};

/** A sheet whose content is short and fixed, so it needs no scrolling root. */
export function SheetScreen({
	title,
	subtitle,
	right,
	children,
}: {
	title: string;
	subtitle?: string;
	right?: ReactNode;
	children: ReactNode;
}) {
	const s = useThemedStyles(makeStyles);
	return (
		<View style={s.sheetScreen}>
			<SheetHeader title={title} subtitle={subtitle} right={right} />
			{children}
		</View>
	);
}

// ---- Grouped settings list --------------------------------------------------
// An iOS-Settings-style grouped list, hand-rolled rather than pulled from
// @expo/ui: that library's Form/Section only exist on its SwiftUI side, so
// Android would need this implementation anyway. Rendering both platforms from
// the same primitives is what keeps them aligned.
