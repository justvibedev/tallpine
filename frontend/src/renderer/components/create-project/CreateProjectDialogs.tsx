import * as Dialog from "@radix-ui/react-dialog";
/** Backdrop and source picker dialog for the create-project flow. */
import { AnimatePresence, motion } from "motion/react";
import { useTranslation } from "react-i18next";
import { createPortal } from "react-dom";
import type { RemoteHost } from "../../hooks/useRemoteHosts";
import { cn } from "../../lib/utils";
import { ProjectSource } from "./create-project-types";
import { ImportSourcePicker } from "./ImportSourcePicker";

export function CreateProjectFlowBackdrop({ open }: { open: boolean }) {
	return createPortal(
		<AnimatePresence>
			{open && (
				<motion.div
					aria-hidden="true"
					className="dialog-overlay z-[calc(var(--z-overlay)-1)]"
					initial={{ opacity: 0 }}
					animate={{ opacity: 1, transition: { duration: 0.08 } }}
					exit={{ opacity: 0, transition: { duration: 0.06 } }}
				/>
			)}
		</AnimatePresence>,
		document.body,
	);
}

export function CreateProjectSourceDialog({
	childOpen,
	closeDisabled,
	disabled,
	hostLabel,
	hostId,
	remoteHosts,
	onOpenChange,
	onCreateStandaloneAgent,
	onSelect,
	onSelectHost,
	open,
}: {
	childOpen: boolean;
	closeDisabled: boolean;
	disabled: boolean;
	hostLabel?: string;
	hostId?: string;
	remoteHosts: readonly RemoteHost[];
	onOpenChange: (open: boolean) => void;
	onCreateStandaloneAgent?: () => void;
	onSelect: (source: ProjectSource) => void;
	onSelectHost?: (hostId?: string) => void;
	open: boolean;
}) {
	const { t } = useTranslation();
	return (
		<Dialog.Root open={open} onOpenChange={onOpenChange}>
			<Dialog.Portal>
				<Dialog.Content
					hidden={childOpen}
					className={cn(
						"fixed left-1/2 top-1/2 z-overlay w-[min(560px,calc(100vw-24px))] -translate-x-1/2 -translate-y-1/2 border-0 bg-transparent p-0 shadow-none outline-none motion-reduce:animate-none",
						childOpen
							? "pointer-events-none opacity-0 animate-modal-out"
							: "data-[state=open]:animate-modal-in data-[state=closed]:animate-modal-out",
					)}
				>
					<Dialog.Title className="sr-only">{hostLabel ? t("remote.addProjectOn", { label: hostLabel, defaultValue: "Add project on {{label}}" }) : t("createProject.addCodeTitle")}</Dialog.Title>
					<Dialog.Description className="sr-only">{t("createProject.addCodeDescription")}</Dialog.Description>
					<div className="flex w-full flex-col items-center gap-3">
						<ImportSourcePicker closeDisabled={closeDisabled} disabled={disabled} hostId={hostId} hostLabel={hostLabel} remoteHosts={remoteHosts} onSelectHost={onSelectHost} onClose={() => onOpenChange(false)} onSelect={onSelect} onCreateStandaloneAgent={onCreateStandaloneAgent} dialog />
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
