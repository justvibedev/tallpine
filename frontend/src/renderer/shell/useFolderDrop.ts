import { useEffect, useRef, useState } from "react";
import { tallpineBridge } from "../lib/bridge";
import { dragCarriesFiles, firstDragEntryIsDirectory } from "../lib/dropClassify";

export function useFolderDrop({ requestCreateProjectFromPath }: {
	requestCreateProjectFromPath: (path: string) => void;
}) {
	// Drop a folder anywhere in the app window to add it as a project, mirroring
	// VS Code's "drop a folder to open it". A depth counter (not a relatedTarget
	// check) tracks dragenter/dragleave so the overlay doesn't flicker as the
	// pointer crosses child-element boundaries. XtermTerminal's own drop handler
	// only swallows (preventDefault/stopPropagation) a drop that is NOT a
	// directory — a dropped folder is left untouched so it bubbles here even
	// when it lands on an active terminal pane.
	const [isDragActive, setIsDragActive] = useState(false);
	const dragDepthRef = useRef(0);
	useEffect(() => {
		const isFileDrag = (event: DragEvent) => dragCarriesFiles(event.dataTransfer);
		const firstEntryIsDirectory = (event: DragEvent) => firstDragEntryIsDirectory(event.dataTransfer);
		const handleDragEnter = (event: DragEvent) => {
			if (!isFileDrag(event)) return;
			event.preventDefault();
			dragDepthRef.current += 1;
			if (dragDepthRef.current === 1 && firstEntryIsDirectory(event)) setIsDragActive(true);
		};
		const handleDragOver = (event: DragEvent) => {
			if (!isFileDrag(event)) return;
			event.preventDefault();
			if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
		};
		const handleDragLeave = (event: DragEvent) => {
			if (!isFileDrag(event)) return;
			dragDepthRef.current = Math.max(0, dragDepthRef.current - 1);
			if (dragDepthRef.current === 0) setIsDragActive(false);
		};
		const handleDrop = (event: DragEvent) => {
			if (!isFileDrag(event)) return;
			event.preventDefault();
			dragDepthRef.current = 0;
			setIsDragActive(false);
			const item = event.dataTransfer?.items?.[0];
			if (!item?.webkitGetAsEntry?.()?.isDirectory) return;
			const file = item.getAsFile();
			if (!file) return;
			// Must be synchronous, on the File taken directly from dataTransfer, in
			// the same tick as the native drop event — see preload.ts's comment.
			const path = tallpineBridge.app.getPathForFile(file);
			if (path) requestCreateProjectFromPath(path);
		};
		window.addEventListener("dragenter", handleDragEnter);
		window.addEventListener("dragover", handleDragOver);
		window.addEventListener("dragleave", handleDragLeave);
		window.addEventListener("drop", handleDrop);
		return () => {
			window.removeEventListener("dragenter", handleDragEnter);
			window.removeEventListener("dragover", handleDragOver);
			window.removeEventListener("dragleave", handleDragLeave);
			window.removeEventListener("drop", handleDrop);
		};
	}, [requestCreateProjectFromPath]);
	return { isDragActive };
}
