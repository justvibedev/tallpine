import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCreateBacklogItem } from "../../hooks/useCreateBacklogItem";
import type { BacklogItem } from "../../lib/backlog-api";
import { Button } from "../ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";

export const CAPTURE_TITLE_MAX = 200;

/** First line is the title, the rest the note; surrounding blank lines are dropped. */
export function splitCapture(text: string): { title: string; note: string } {
	const trimmed = text.replace(/^\s+/, "");
	const newline = trimmed.indexOf("\n");
	if (newline < 0) return { title: trimmed.trim(), note: "" };
	return { title: trimmed.slice(0, newline).trim(), note: trimmed.slice(newline + 1).trim() };
}

type BacklogCapturePopoverProps = {
	open: boolean;
	initialText: string;
	projects: ReadonlyArray<{ id: string; name: string }>;
	defaultProjectId: string | undefined;
	onOpenChange: (open: boolean) => void;
	onAdded: (item: BacklogItem) => void;
};

/**
 * Quick capture: type a title and press Enter; Shift+Enter starts a note. It never
 * touches a session, so it is safe to open over a running terminal or the browser.
 */
export function BacklogCapturePopover({ open, initialText, projects, defaultProjectId, onOpenChange, onAdded }: BacklogCapturePopoverProps) {
	const { t } = useTranslation();
	const fieldId = useId();
	const [text, setText] = useState(initialText);
	const [projectId, setProjectId] = useState(defaultProjectId ?? projects[0]?.id ?? "");
	const [error, setError] = useState<string | null>(null);
	const create = useCreateBacklogItem(projectId);

	// A fresh capture starts from what the caller typed and its project.
	useEffect(() => {
		if (!open) return;
		setText(initialText);
		setError(null);
		setProjectId(defaultProjectId ?? projects[0]?.id ?? "");
		// eslint-disable-next-line react-hooks/exhaustive-deps -- reset only when the popover opens
	}, [open]);

	const { title, note } = splitCapture(text);
	const submit = async () => {
		if (title === "" || create.isPending) return;
		if ([...title].length > CAPTURE_TITLE_MAX) {
			setError(t("backlog.capture.tooLong", { max: CAPTURE_TITLE_MAX }));
			return;
		}
		setError(null);
		try {
			const item = await create.mutateAsync({ title, note });
			onOpenChange(false);
			onAdded(item);
		} catch (failure) {
			setError(t("backlog.capture.error", { message: failure instanceof Error ? failure.message : String(failure) }));
		}
	};

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent showCloseButton={false} className="top-[22%] max-w-xl translate-y-0 gap-3">
				<DialogTitle className="text-sm">{t("backlog.capture.title")}</DialogTitle>
				<DialogDescription className="sr-only">{t("backlog.capture.description")}</DialogDescription>
				<label htmlFor={fieldId} className="sr-only">{t("backlog.capture.label")}</label>
				<textarea
					id={fieldId}
					autoFocus
					rows={4}
					value={text}
					onChange={(event) => setText(event.target.value)}
					onKeyDown={(event) => {
						// Enter adds; Shift+Enter (and IME composition) keep the native newline.
						if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
							event.preventDefault();
							void submit();
						}
					}}
					placeholder={t("backlog.capture.placeholder")}
					className="w-full resize-none rounded-md bg-input/50 px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
				/>
				{error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
				<div className="flex items-center gap-2">
					<Select value={projectId} onValueChange={setProjectId} disabled={projects.length === 0}>
						<SelectTrigger size="sm" aria-label={t("backlog.scope.label")} className="max-w-48">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							{projects.map((project) => <SelectItem key={project.id} value={project.id}>{project.name}</SelectItem>)}
						</SelectContent>
					</Select>
					<span className="ml-auto text-xs text-muted-foreground">{t("backlog.capture.hint")}</span>
					<Button type="button" size="sm" disabled={title === "" || projectId === "" || create.isPending} onClick={() => void submit()}>
						{t("backlog.capture.add")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
