import { TriangleAlert, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";

export function DismissButton({ label, onClick, disabled, className }: { label: string; onClick: () => void; disabled?: boolean; className?: string }) {
	return (
		<button type="button" aria-label={label} title={label} disabled={disabled} onClick={onClick}
			className={cn("shrink-0 rounded p-0.5 text-muted-foreground hover:bg-interactive-active hover:text-foreground", className)}>
			<X aria-hidden="true" className="size-3.5" />
		</button>
	);
}

/** An error from a switch, shown as a strip on top of the chat box. */
export function ComposerErrorNotice(
	{ title, message, onDismiss, dismissLabel }: { title: string; message: string; onDismiss: () => void; dismissLabel?: string },
) {
	const { t } = useTranslation();
	return (
		<div
			role="alert"
			className="-mb-2 flex min-w-0 items-start gap-2 rounded-t-(--radius-chat-composer) border-b border-destructive/30 bg-destructive/10 px-3 py-2 text-xs"
		>
			<TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-destructive" />
			<div className="min-w-0 flex-1">
				<p className="font-medium text-foreground">{title}</p>
				<p className="break-words text-muted-foreground">{message}</p>
			</div>
			<DismissButton label={dismissLabel ?? t("agentSwitch.dismiss")} onClick={onDismiss} />
		</div>
	);
}
