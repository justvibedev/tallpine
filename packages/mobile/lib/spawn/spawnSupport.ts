import { StyleSheet } from "react-native";
import { ApiError } from "../api";
import { userFacingError } from "../connectionError";
import { chatErrorCopy, isChatPreflightError } from "../chatError";
import type { Theme } from "../theme";
import { space, type } from "../tokens";

export { SheetErrorBoundary as ErrorBoundary } from "../RouteErrorBoundary";


// Human copy for a failed spawn, matching every other screen. Never the wire
// string ("401 Unauthorized - missing or invalid connection password").
export function spawnErrorCopy(e: unknown): string {
	if (isChatPreflightError(e)) return chatErrorCopy(e);
	if (e instanceof ApiError && e.code === "PROMPT_TOO_LONG") {
		return "Task prompt is too long. Keep it to 16 KiB or fewer (emoji and other non-English characters use more than one byte). Shorten it and try again.";
	}
	return userFacingError(e, "Couldn't start the worker. Try again.");
}

// Android's compact field height and the iOS host's minimum layout height.
export const PROMPT_MIN_HEIGHT = 112;

export const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		content: { flex: 1, paddingHorizontal: space.lg, paddingTop: space.lg, paddingBottom: space.sm, gap: space.sm },
		iosContent: { paddingTop: space.xxxl },
		androidModalRoot: { flex: 1, backgroundColor: "transparent" },
		androidSheet: {
			paddingTop: space.xs,
			paddingBottom: space.md,
			backgroundColor: t.bgBase,
		},
		androidContent: { flex: 0, paddingTop: space.md, paddingBottom: space.none },
		messages: { gap: space.xs },
		voice: { flexDirection: "row", alignItems: "center", gap: space.xs, backgroundColor: t.tintRed, borderRadius: 8, paddingHorizontal: space.sm, paddingVertical: space.xs },
		voiceText: { fontFamily: "Geist_400Regular", flex: 1, color: t.textSecondary, fontSize: type.caption2.fontSize },
		promptHost: { width: "100%", height: PROMPT_MIN_HEIGHT },
		promptHostFill: { height: undefined, flex: 1, minHeight: 0 },
		warn: { fontFamily: "Geist_400Regular", color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		error: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	});
