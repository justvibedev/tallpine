import * as Dialog from "@radix-ui/react-dialog";
/** Import source picker. */
import { useTranslation } from "react-i18next";
import { FolderClosed, Folders, GitFork, MessageSquarePlus, X } from "lucide-react";
import { type ReactNode } from "react";
import type { RemoteHost } from "../../hooks/useRemoteHosts";
import { onboardingPanelClass, onboardingPanelDescriptionClass, onboardingPanelTitleClass } from "../../lib/onboarding-ui";
import { Label } from "../ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { ProjectSource } from "./create-project-types";

/** Shared source chooser for first-run and subsequent project creation. */
export function ImportSourcePicker({
	closeDisabled,
	dialog = false,
	disabled,
	hostLabel,
	hostId,
	remoteHosts = [],
	onClose,
	onCreateStandaloneAgent,
	onSelect,
	onSelectHost,
}: {
	closeDisabled?: boolean;
	dialog?: boolean;
	disabled: boolean;
	hostLabel?: string;
	hostId?: string;
	remoteHosts?: readonly RemoteHost[];
	onClose?: () => void;
	onCreateStandaloneAgent?: () => void;
	onSelect: (source: ProjectSource) => void;
	onSelectHost?: (hostId?: string) => void;
}) {
	const { t } = useTranslation();
	const createStandaloneAgent = () => {
		onClose?.();
		onCreateStandaloneAgent?.();
	};
	const localSources: Array<{ source: ProjectSource; icon: ReactNode; label: string; description: string }> = [
		{
			source: "clone",
			icon: <GitFork className="size-5" aria-hidden="true" strokeWidth={1.8} />,
			label: t("createProject.cloneFromGit"),
			description: t("createProject.cloneFromGitDesc"),
		},
		{
			source: "local",
			icon: <FolderClosed className="size-5" aria-hidden="true" strokeWidth={1.8} />,
			label: t("createProject.openLocal"),
			description: hostLabel ? t("remote.openExistingProjectDesc", { label: hostLabel }) : t("createProject.openLocalDesc"),
		},
		{
			source: "workspace",
			icon: <Folders className="size-5" aria-hidden="true" strokeWidth={1.8} />,
			label: t("createProject.addWorkspace"),
			description: t("createProject.workspaceDesc"),
		},
	];
	return (
		<div className={onboardingPanelClass}>
			{dialog ? (
				<Dialog.Title className={onboardingPanelTitleClass}>{hostLabel ? t("remote.addProjectOn", { label: hostLabel, defaultValue: "Add project on {{label}}" }) : t("createProject.addCodeTitle")}</Dialog.Title>
			) : (
				<h2 className={onboardingPanelTitleClass}>{hostLabel ? t("remote.addProjectOn", { label: hostLabel, defaultValue: "Add project on {{label}}" }) : t("createProject.addCodeTitle")}</h2>
			)}
			{dialog ? (
				<Dialog.Description className={onboardingPanelDescriptionClass}>
					{hostLabel ? t("remote.projectPathHint", { label: hostLabel, defaultValue: "Use a repository folder on {{label}}, or clone one there from Git." }) : t("createProject.addCodeDescription")}
				</Dialog.Description>
			) : (
				<p className={onboardingPanelDescriptionClass}>{hostLabel ? t("remote.projectPathHint", { label: hostLabel, defaultValue: "Use a repository folder on {{label}}, or clone one there from Git." }) : t("createProject.addCodeDescription")}</p>
			)}
			{remoteHosts.length > 0 && onSelectHost && <div className="mx-4 mb-4 flex items-center justify-between gap-3">
				<Label htmlFor="create-project-machine">{t("createProject.machine")}</Label>
				<Select value={hostId ?? "__local__"} onValueChange={(value) => onSelectHost(value === "__local__" ? undefined : value)}>
					<SelectTrigger id="create-project-machine" className="max-w-[65%]"><SelectValue /></SelectTrigger>
					<SelectContent position="popper" side="bottom" align="end" sideOffset={4}>
						<SelectItem value="__local__">{t("settings.harness.thisComputer")}</SelectItem>
						{remoteHosts.map((host) => <SelectItem key={host.hostId} value={host.hostId} disabled={host.status !== "connected"}>{host.label}</SelectItem>)}
					</SelectContent>
				</Select>
			</div>}
			<div className="mx-4 mb-4 flex flex-col gap-3">
				<div className="overflow-hidden rounded-md border border-[var(--color-border-import-modal)] bg-[var(--color-bg-import-modal)]">
					<div className="flex flex-col">
						{localSources.map(({ source, icon, label, description }) => (
							<button
								key={source}
								type="button"
								className="group flex min-h-[76px] items-center gap-3 border-b border-[var(--color-border-import-modal)] px-3.5 py-3 text-left hover:bg-accent/50 active:bg-accent disabled:pointer-events-none disabled:opacity-50 last:border-b-0"
								aria-label={label}
								disabled={disabled}
								onClick={() => onSelect(source)}
							>
								<span className="grid w-9 shrink-0 place-items-center text-muted-foreground group-hover:text-foreground">
									{icon}
								</span>
								<span className="min-w-0">
									<span className="block text-[14px] font-medium text-foreground">{label}</span>
									<span className="mt-0.5 block text-[12px] leading-5 text-muted-foreground">{description}</span>
								</span>
							</button>
						))}
						{onCreateStandaloneAgent ? (
							<button type="button" className="group flex min-h-[76px] items-center gap-3 px-3.5 py-3 text-left hover:bg-accent/50 active:bg-accent disabled:pointer-events-none disabled:opacity-50" aria-label={t("home.newStandaloneAgent")} disabled={disabled} onClick={createStandaloneAgent}>
								<span className="grid w-9 shrink-0 place-items-center text-muted-foreground group-hover:text-foreground">
									<MessageSquarePlus className="size-5" aria-hidden="true" strokeWidth={1.8} />
								</span>
								<span><span className="block text-sm font-medium">{t("home.newStandaloneAgent")}</span><span className="mt-0.5 block text-[12px] leading-5 text-muted-foreground">{t("createProject.standaloneDesc")}</span></span>
							</button>
						) : null}
					</div>
				</div>
			</div>
			{dialog && onClose ? (
				<button
					type="button"
					className="settings-close-button absolute right-3 top-3"
					aria-label={t("createProject.closeDialog")}
					disabled={closeDisabled ?? disabled}
					onClick={onClose}
				>
					<X className="size-4" aria-hidden="true" />
				</button>
			) : null}
		</div>
	);
}
