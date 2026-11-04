/** Lazy file explorer and workspace chunks shared by SessionView and its body. */
import { lazy } from "react";

// The files explorer, review pane and file editor (diff renderer, file tree,
// CodeMirror, the file-icon set: several MB) load the first time a session
// shows files. Chat never uses them, so they stay off the session-open path.
export const SessionFileExplorer = lazy(() =>
	import("../SessionFileExplorer").then((module) => ({ default: module.SessionFileExplorer })),
);
export const SessionFileWorkspace = lazy(() =>
	import("../SessionFileWorkspace").then((module) => ({ default: module.SessionFileWorkspace })),
);
