import { WorkerControlsSheetBody, type WorkerControlsSheetProps } from "./worker-controls-sheet.body";

export function WorkerControlsSheet(props: WorkerControlsSheetProps) {
	return <WorkerControlsSheetBody {...props} variant={{ android: false }} />;
}
