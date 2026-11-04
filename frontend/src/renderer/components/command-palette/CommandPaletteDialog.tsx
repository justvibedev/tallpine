import { ArrowLeft, Loader2 } from "lucide-react";
import { iconForCommand } from "../../lib/command-palette-icons";
import { Button } from "../ui/button";
import { TaskComposer } from "../TaskComposer";
import { CommandDialog, CommandEmpty, CommandFooter, CommandGroup, CommandInput, CommandItem, CommandList } from "../ui/command";
import { Spinner } from "../ui/spinner";
import type { CommandPaletteState } from "./palette-types";
import { PALETTE_FILE_SEARCH_MIN_CHARS } from "./palette-constants";

export function CommandPaletteDialog({ v }: { v: CommandPaletteState }) {
	const {
		activeFileTarget,
		composerWrapRef,
		confirmDiscard,
		error,
		fileItems,
		fileSearchQuery,
		groups,
		handlePaletteAnimationEnd,
		handleTaskCreated,
		isOpen,
		keepEditingRef,
		normalizedFileQuery,
		onComposerDirtyChange,
		onComposerSubmittingChange,
		onSelectItem,
		pendingDismiss,
		pendingId,
		query,
		requestDismiss,
		scoped,
		setError,
		setOpen,
		setPendingDismiss,
		setQuery,
		setSelectedValue,
		t,
		value,
		view,
		viewRef,
		workspaces,
	} = v;
	const fileSearchSession = view.mode === "file-search"
		? workspaces
			.find((workspace) => workspace.id === view.target.projectId
				&& (workspace.hostId ?? "") === (view.target.hostId ?? ""))
			?.sessions.find((session) => session.id === view.target.sessionId)
		: undefined;
	const contextLabel =
		view.mode === "session-actions"
			? (scoped?.session.title ?? t("command.sessionFallback"))
			: view.mode === "new-task"
				? t("command.newTask")
				: view.mode === "file-session-picker"
					? t("command.chooseFileSession")
					: view.mode === "file-search"
						? (fileSearchSession?.title ?? t("command.sessionFallback"))
						: "";
	const fileSearchActive = view.mode === "file-search"
		|| (view.mode === "root" && Boolean(activeFileTarget) && query.trim().length >= PALETTE_FILE_SEARCH_MIN_CHARS);
	const fileSearchSettled = normalizedFileQuery === query.trim();
	const fileSearchError = fileSearchActive && fileSearchSettled && fileSearchQuery.error
		? (fileSearchQuery.error instanceof Error ? fileSearchQuery.error.message : t("command.searchFilesError"))
		: null;

	return (
				<CommandDialog
					// CommandDialog supplies an overlay plus trapped focus without the
					// body-wide scroll/pointer lock that made palette opening scale with
					// every retained shell node.
					modal={false}
					open={isOpen}
				onOpenChange={(open) => (open ? setOpen(true) : requestDismiss("close"))}
					contentProps={{
						onAnimationEnd: handlePaletteAnimationEnd,
					onEscapeKeyDown: (event) => {
						event.preventDefault();
						if (event.isComposing) return;
						if (pendingDismiss !== null) {
							setPendingDismiss(null);
							return;
						}
						requestDismiss(viewRef.current.mode === "root" ? "close" : "pop");
					},
				}}
				commandProps={{
					shouldFilter: false,
					value,
					onValueChange: setSelectedValue,
					loop: true,
					label: t("command.palette"),
				}}
			>
				{view.mode !== "root" && (
					<div className="flex items-center gap-2 border-b border-border px-3 py-2">
						<button
							type="button"
							onClick={() => requestDismiss("pop")}
							className="grid size-10 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-surface hover:text-foreground"
							aria-label={t("command.back")}
						>
							<ArrowLeft className="size-icon-base" aria-hidden="true" />
						</button>
						<span className="min-w-0 truncate rounded-md bg-surface px-2 py-0.5 text-2xs font-medium text-muted-foreground">
							{contextLabel}
						</span>
					</div>
				)}

				{view.mode === "new-task" ? (
					<div
						ref={composerWrapRef}
						// cmdk must not see composer keystrokes, but Tab has to reach the dialog's focus trap.
						onKeyDown={(event) => { if (event.key !== "Tab") event.stopPropagation(); }}
					>
						{pendingDismiss !== null && (
							<div role="alert" className="mx-3 mt-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-foreground">
								<p className="text-muted-foreground">{t("command.discardDraft")}</p>
								<div className="mt-2 flex justify-end gap-3">
									<Button ref={keepEditingRef} type="button" variant="footer" onClick={() => setPendingDismiss(null)}>
										{t("command.keepEditing")}
									</Button>
									<Button type="button" variant="footer" className="text-destructive" onClick={confirmDiscard}>
										{t("command.discard")}
									</Button>
								</div>
							</div>
						)}
						<TaskComposer
							projectId={view.projectId}
							autoFocusTitle
							onDirtyChange={onComposerDirtyChange}
							onSubmittingChange={onComposerSubmittingChange}
							onCreated={(sessionId) => void handleTaskCreated(view.projectId, sessionId)}
						/>
					</div>
				) : (
					<>
						<CommandInput
							value={query}
								onValueChange={(next) => {
									setQuery(next);
									setError(null);
								}}
							placeholder={
								view.mode === "session-actions"
									? t("command.searchActionsPlaceholder")
									: view.mode === "file-session-picker"
										? t("command.chooseFileSession")
										: view.mode === "file-search"
											? t("command.searchFilesPlaceholder", { session: fileSearchSession?.title ?? t("command.sessionFallback") })
											: t("command.searchPlaceholder")
							}
							onKeyDown={(event) => {
								if (
									event.key === "Backspace" &&
									query === "" &&
									!event.nativeEvent.isComposing &&
									viewRef.current.mode !== "root"
								) {
									event.preventDefault();
									requestDismiss("pop");
								}
							}}
						/>
						<CommandList>
							<CommandEmpty>
								{view.mode === "file-search"
									? query.trim().length < PALETTE_FILE_SEARCH_MIN_CHARS
										? t("command.searchFilesHint")
										: fileSearchQuery.isFetching || !fileSearchSettled
											? t("command.searchFilesLoading")
											: t("command.searchFilesEmpty")
									: t("command.noResults")}
							</CommandEmpty>
							{error && (
								<div
									role="alert"
									className="mx-1 mb-1 overflow-hidden rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs wrap-break-word text-destructive"
								>
									{error}
								</div>
							)}
							{fileSearchError && (
								<div role="alert" className="mx-1 mb-1 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
									{fileSearchError}
								</div>
							)}
							{view.mode === "file-search" && fileSearchQuery.isFetching && fileItems.length > 0 && (
								<div role="status" className="px-3 py-1 text-xs text-muted-foreground">{t("command.searchFilesLoading")}</div>
							)}
							{groups.map((group) => (
								<CommandGroup key={group.id} heading={group.label || undefined}>
									{group.items.map((item) => {
										const Icon = iconForCommand(item);
										return (
										<CommandItem
											key={item.id}
											value={item.id}
											disabled={item.disabled || (pendingId !== null && pendingId !== item.id)}
											onSelect={() => onSelectItem(item)}
										>
											{Icon ? <Icon strokeWidth={1.75} aria-hidden="true" /> : null}
											<span className="min-w-0 flex-1 truncate">{item.title}</span>
											{pendingId === item.id ? (
												<Spinner icon={Loader2} className="ml-auto size-3.5 text-[var(--color-text-command-muted)]" />
											) : item.disabled && item.disabledReason ? (
												<span className="ml-auto text-control text-[var(--color-text-command-muted)]">
													{item.disabledReason}
												</span>
											) : item.subtitle ? (
												<span className="ml-auto max-w-command-subtitle truncate text-control text-[var(--color-text-command-muted)]">
													{item.subtitle}
												</span>
											) : null}
										</CommandItem>
										);
									})}
								</CommandGroup>
							))}
							{fileSearchActive && fileSearchSettled && fileSearchQuery.data?.truncated && (
								<div role="status" className="px-3 py-2 text-xs text-muted-foreground">{t("command.searchFilesTruncated")}</div>
							)}
						</CommandList>
						<CommandFooter aria-hidden="true">
							<span className="inline-flex items-center gap-1.5">
								<span>↑↓</span>
								<span>{t("command.select")}</span>
							</span>
							<span className="inline-flex items-center gap-1.5">
								<span>↵</span>
								<span>{t("command.open")}</span>
							</span>
						</CommandFooter>
					</>
				)}
			</CommandDialog>
	);
}
