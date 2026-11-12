/** True when a drag carries files from outside the page (not text, links or in-app drags). */
export function dragCarriesFiles(dataTransfer: Pick<DataTransfer, "types"> | null | undefined): boolean {
	return Array.from(dataTransfer?.types ?? []).includes("Files");
}

/** True when the first dragged entry is a folder, which the app-wide folder drop turns into a project. */
export function firstDragEntryIsDirectory(dataTransfer: Pick<DataTransfer, "items"> | null | undefined): boolean {
	return dataTransfer?.items?.[0]?.webkitGetAsEntry?.()?.isDirectory ?? false;
}
