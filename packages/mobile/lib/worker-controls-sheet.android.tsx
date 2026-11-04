import { WorkerControlsSheetBody, type WorkerControlsSheetProps } from "./worker-controls-sheet.body";

// @expo/ui fires Android onClose as soon as the sheet is told to close, while
// its window is still animating out. Focusing search then leaves the field
// focused without a keyboard (reproduced on a Samsung M31), so wait for the
// hide to finish, as the sheet did before it moved to onClose.
const ANDROID_SHEET_HIDE_MS = 180;

export function WorkerControlsSheet(props: WorkerControlsSheetProps) {
	return <WorkerControlsSheetBody {...props} variant={{ android: true, searchDelayMs: ANDROID_SHEET_HIDE_MS }} />;
}
