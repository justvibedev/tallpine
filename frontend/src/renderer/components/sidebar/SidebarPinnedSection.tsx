import { useTranslation } from "react-i18next";
import { type WorkspaceSession } from "../../types/workspace";
import { SidebarMenuSub } from "../ui/sidebar";
import type { RemoteHost } from "../../hooks/useRemoteHosts";
import { sessionUiKey } from "../../lib/hosts";
import { AnimatedSectionBody } from "./AnimatedSectionBody";
import { PinnedSessionRow } from "./PinnedSessionRow";
import { SectionDisclosure } from "./SectionDisclosure";
import { type Selection } from "./types";

export function SidebarPinnedSection({ pinnedSessions, pinnedOpen, setPinnedOpen, selection, remoteHosts, openPinnedSession, handlePinnedSessionKilled }: { pinnedSessions: WorkspaceSession[]; pinnedOpen: boolean; setPinnedOpen: (update: (open: boolean) => boolean) => void; selection: Selection; remoteHosts: RemoteHost[]; openPinnedSession: (session: WorkspaceSession) => void; handlePinnedSessionKilled: (session: WorkspaceSession) => void }) {
	const { t } = useTranslation();
	return (
		<>
		{/* Pinned — collapsible; hidden when empty. */}
		{pinnedSessions.length > 0 && (
			<div className="sidebar-expanded-chrome flex shrink-0 flex-col group-data-[collapsible=icon]:hidden">
				<SectionDisclosure
					label={t("shell.pinned")}
					open={pinnedOpen}
					onToggle={() => setPinnedOpen((v) => !v)}
					className="mb-1"
				/>
				<AnimatedSectionBody open={pinnedOpen}>
					<SidebarMenuSub
						className="sidebar-expanded-chrome mx-0 ml-0 translate-x-0 gap-0.5 border-l-0 px-0 py-0.5 mb-2"
						data-testid="pinned-session-list"
					>
						{pinnedSessions.map((session) => (
							<PinnedSessionRow
								key={sessionUiKey(session.id, session.hostId)}
								session={session}
								active={session.hostId
									? selection.activeRemoteHostId === session.hostId && selection.activeRemoteSessionId === session.id
									: selection.activeSessionId === session.id}
								hostLabel={session.hostId ? remoteHosts.find((host) => host.hostId === session.hostId)?.label ?? session.hostId : undefined}
								onKilled={handlePinnedSessionKilled}
								onOpenSession={openPinnedSession}
							/>
						))}
					</SidebarMenuSub>
				</AnimatedSectionBody>
			</div>
		)}
		</>
	);
}
