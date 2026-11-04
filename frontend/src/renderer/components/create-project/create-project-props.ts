/** Props of the create-project flow. */
import { type ReactNode } from "react";
import type { RemoteHost } from "../../hooks/useRemoteHosts";
import type { CreateProjectInput, CloneProjectInput } from "../CreateProjectFlow";
import type { ProjectSource } from "./create-project-types";
import { CreateProjectFlowMode } from "./create-project-types";

export type CreateProjectFlowProps = {
	children?: (state: { choosePath: () => void; disabled: boolean; error: string | null; label: string }) => ReactNode;
	connected?: boolean;
	existingProjectNames?: readonly string[];
	existingProjectPaths?: readonly string[];
	// A folder was dropped on the app window (ShellLayout owns the global
	// listener). Mirrors openSignal but carries a path: skips straight to the
	// mode picker with the native OS dialog step skipped.
	droppedPath?: { path: string; nonce: number } | null;
	// When true, render the Workspace/Project chooser inline (start page) instead
	// of behind a trigger + dialog. Folder validation + agent sheet stay modal.
	embedded?: boolean;
	idleLabel?: string;
	hostId?: string;
	hostLabel?: string;
	remoteHosts?: readonly RemoteHost[];
	initialOpen?: boolean;
	mode?: CreateProjectFlowMode;
	onCloneProject?: (input: CloneProjectInput) => Promise<void>;
	onCreateProject: (input: CreateProjectInput) => Promise<void>;
	onInitializeProject: (path: string) => Promise<void>;
	onCreateStandaloneAgent?: () => void;
	onDismiss?: () => void;
	onOpenExistingProject?: (path: string) => void | Promise<void>;
	onSelectHost?: (hostId?: string) => void;
	// Monotonic counter: each new value opens the flow programmatically (the ⌘N
	// "no project in scope" fallback). Lets the shortcut reuse the sidebar's own
	// create-project flow instead of a separate delegating component.
	openSignal?: number;
	// Home-page action cards: each new nonce jumps straight to its source.
	sourceSignal?: { source: ProjectSource; nonce: number } | null;
};
