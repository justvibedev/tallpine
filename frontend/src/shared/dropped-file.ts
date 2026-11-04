/** Largest file the terminal accepts by drag-and-drop (bytes cross IPC in memory). */
export const MAX_DROPPED_FILE_BYTES = 64 * 1024 * 1024;

export function assertDroppedFileSize(bytes: unknown): asserts bytes is Uint8Array {
	if (!(bytes instanceof Uint8Array)) throw new Error("Dropped file is invalid.");
	if (bytes.byteLength > MAX_DROPPED_FILE_BYTES) throw new Error("Dropped file is too large.");
}
