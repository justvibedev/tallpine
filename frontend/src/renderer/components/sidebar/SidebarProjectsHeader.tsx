import { useTranslation } from "react-i18next";
import type { RemoteHost } from "../../hooks/useRemoteHosts";
import { CreateProjectButton } from "./CreateProjectButton";
import { SectionDisclosure } from "./SectionDisclosure";
import { type SidebarProps } from "./types";

export function SidebarProjectsHeader({ projectsOpen, setProjectsOpen, existingProjectPaths, remoteHosts, onCreateRemoteProject, onInitializeRemoteProject, onCloneProject, onCreateProject, onInitializeProject, openExistingProject }: { projectsOpen: boolean; setProjectsOpen: (update: (open: boolean) => boolean) => void; existingProjectPaths: string[]; remoteHosts: RemoteHost[]; onCreateRemoteProject: SidebarProps["onCreateRemoteProject"]; onInitializeRemoteProject: SidebarProps["onInitializeRemoteProject"]; onCloneProject: SidebarProps["onCloneProject"]; onCreateProject: SidebarProps["onCreateProject"]; onInitializeProject: SidebarProps["onInitializeProject"]; openExistingProject: (path: string) => void }) {
	const { t } = useTranslation();
	return (
		<>
		{/* Projects — collapsible; the "+" stays mounted through the empty
		    state so it keeps owning the ⌘N create flow. */}
		<div className="sidebar-expanded-chrome flex shrink-0 pb-0.5 group-data-[collapsible=icon]:hidden">
			<SectionDisclosure
				label={t("shell.projects")}
				open={projectsOpen}
				onToggle={() => setProjectsOpen((open) => !open)}
				trailing={
					<CreateProjectButton
						existingProjectPaths={existingProjectPaths}
						remoteHosts={remoteHosts}
						onCreateRemoteProject={onCreateRemoteProject}
						onInitializeRemoteProject={onInitializeRemoteProject}
						onCloneProject={onCloneProject}
						onCreateProject={onCreateProject}
						onInitializeProject={onInitializeProject}
						onOpenExistingProject={openExistingProject}
					/>
				}
			/>
		</div>
		</>
	);
}
