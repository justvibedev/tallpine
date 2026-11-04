import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "../ConfirmDialog";
import { Input } from "../ui/input";

type BacklogHoldDialogProps = {
	open: boolean;
	itemRef: string;
	onOpenChange: (open: boolean) => void;
	/** Resolves once the hold is saved; a rejection leaves the dialog open with the message. */
	onHold: (reason: string) => Promise<unknown>;
};

/** Free-text reason for holding an item. A held item is skipped by "do the next item". */
export function BacklogHoldDialog({ open, itemRef, onOpenChange, onHold }: BacklogHoldDialogProps) {
	const { t } = useTranslation();
	const [reason, setReason] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | undefined>();
	const submit = async () => {
		setBusy(true);
		setError(undefined);
		try {
			await onHold(reason.trim());
			setReason("");
			onOpenChange(false);
		} catch (failure) {
			setError(t("backlog.saveError", { message: failure instanceof Error ? failure.message : String(failure) }));
		} finally {
			setBusy(false);
		}
	};
	return (
		<ConfirmDialog
			open={open}
			title={t("backlog.hold.title", { ref: itemRef })}
			description={t("backlog.hold.description")}
			confirmLabel={t("backlog.action.hold")}
			confirmDisabled={reason.trim() === ""}
			busy={busy}
			error={error}
			onConfirm={() => void submit()}
			onOpenChange={onOpenChange}
		>
			<Input
				autoFocus
				aria-label={t("backlog.hold.reason")}
				placeholder={t("backlog.hold.placeholder")}
				maxLength={200}
				value={reason}
				onChange={(event) => setReason(event.target.value)}
				onKeyDown={(event) => event.key === "Enter" && reason.trim() !== "" && void submit()}
			/>
		</ConfirmDialog>
	);
}
