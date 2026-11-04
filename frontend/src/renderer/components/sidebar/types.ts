import { type RefObject } from "react";
import { type WorkspaceSummary } from "../../types/workspace";
import { type CloneProjectInput, type CreateProjectInput } from "../CreateProjectFlow";
import type { RemoteHost } from "../../hooks/useRemoteHosts";
import { useSelection } from "./useSelection";

export type SidebarProps = {
	underTopbar?: boolean;
	/** Chrome height to clear when underTopbar is set. Defaults to --size-toolbar. */
	topbarOffset?: "toolbar" | "titlebar" | "trafficLights" | "session";
	workspaceError?: string;
	workspaces: WorkspaceSummary[];
	remoteHosts?: RemoteHost[];
	onCreateRemoteProject: (hostId: string, input: CreateProjectInput) => Promise<void>;
	onInitializeRemoteProject: (hostId: string, path: string) => Promise<void>;
	onOpenRemoteProject?: (hostId: string, projectId: string) => void;
	onOpenRemoteOrchestrator?: (hostId: string, projectId: string) => void;
	onConfigureRemoteProject?: (hostId: string, projectId: string) => void;
	onRemoveRemoteProject?: (hostId: string, projectId: string) => Promise<void>;
	onRetryRemoteHosts?: () => void;
	remoteWorkspaces?: WorkspaceSummary[];
	remoteFailedHostIds?: string[];
	onCloneProject: (input: CloneProjectInput) => Promise<void>;
	onCreateProject: (input: CreateProjectInput) => Promise<void>;
	onInitializeProject: (path: string) => Promise<void>;
	onRemoveProject: (projectId: string) => Promise<void>;
	/** Fixed shell chrome that also consumes the live sidebar width. */
	resizeAuxiliaryTargetRef?: RefObject<HTMLElement | null>;
};


export type Selection = ReturnType<typeof useSelection>;
