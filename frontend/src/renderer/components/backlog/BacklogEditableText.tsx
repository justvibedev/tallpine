import { Pencil } from "lucide-react";
import { useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChatMarkdown } from "../chat/ChatMarkdown";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

type BacklogEditableTextProps = {
	label: string;
	value: string;
	emptyLabel?: string;
	multiline?: boolean;
	markdown?: boolean;
	/** The title: shown as a heading, never saved empty. */
	heading?: boolean;
	maxLength?: number;
	/** Resolves when the server accepted the change; a rejection keeps the editor open and shows its message. */
	onSave: (value: string) => Promise<unknown>;
};

/** Read-first text with an explicit Edit button; Save and Cancel appear only while editing. Ctrl/Cmd+Enter saves, Escape cancels. */
export function BacklogEditableText({ label, value, emptyLabel, multiline = false, markdown = false, heading = false, maxLength, onSave }: BacklogEditableTextProps) {
	const { t } = useTranslation();
	const id = useId();
	const [draft, setDraft] = useState<string | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [saving, setSaving] = useState(false);
	const editButton = useRef<HTMLButtonElement>(null);
	const editing = draft !== null;

	const close = () => {
		setDraft(null);
		setError(null);
		requestAnimationFrame(() => editButton.current?.focus());
	};
	const save = async () => {
		if (draft === null) return;
		const next = draft.trim();
		if (next === value || (heading && next === "")) return close();
		setSaving(true);
		try {
			await onSave(next);
			close();
		} catch (failure) {
			setError(t("backlog.saveError", { message: failure instanceof Error ? failure.message : String(failure) }));
		} finally {
			setSaving(false);
		}
	};
	const onKeyDown = (event: React.KeyboardEvent) => {
		if (event.key === "Escape") {
			event.stopPropagation();
			close();
		} else if (event.key === "Enter" && (!multiline || event.metaKey || event.ctrlKey)) {
			event.preventDefault();
			void save();
		}
	};

	return (
		<div className="group/field">
			<div className="flex items-center gap-1">
				<label htmlFor={editing ? id : undefined} className={heading ? "sr-only" : "text-xs font-medium text-muted-foreground"}>
					{label}
				</label>
				{!editing ? (
					<Button
						ref={editButton}
						type="button"
						variant="ghost"
						size="icon-sm"
						aria-label={t("backlog.detail.edit", { field: label })}
						onClick={() => setDraft(value)}
						className="ml-auto opacity-0 focus-visible:opacity-100 group-hover/field:opacity-100"
					>
						<Pencil aria-hidden="true" className="size-3.5" />
					</Button>
				) : null}
			</div>
			{editing ? (
				<div className="space-y-2">
					{multiline ? (
						<textarea
							id={id}
							autoFocus
							rows={Math.min(14, Math.max(4, draft.split("\n").length + 1))}
							value={draft}
							maxLength={maxLength}
							onChange={(event) => setDraft(event.target.value)}
							onKeyDown={onKeyDown}
							className="w-full resize-y rounded-md bg-input/50 px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						/>
					) : (
						<Input id={id} autoFocus value={draft} maxLength={maxLength} onChange={(event) => setDraft(event.target.value)} onKeyDown={onKeyDown} />
					)}
					{error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
					<div className="flex items-center gap-2">
						<Button type="button" size="sm" variant="secondary" disabled={saving} onClick={() => void save()}>{t("backlog.detail.save")}</Button>
						<Button type="button" size="sm" variant="ghost" disabled={saving} onClick={close}>{t("backlog.detail.cancel")}</Button>
					</div>
				</div>
			) : value === "" ? (
				<p className="text-sm text-muted-foreground">{emptyLabel}</p>
			) : markdown ? (
				<ChatMarkdown text={value} className="text-sm" />
			) : heading ? (
				<h2 className="text-base font-medium text-foreground">{value}</h2>
			) : (
				<p className="whitespace-pre-wrap text-sm">{value}</p>
			)}
		</div>
	);
}
