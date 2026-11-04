import { CodeView } from "@pierre/diffs/react";
import { ChevronRight, FileCode2, GitCompareArrows, MessageSquarePlus, MoreVertical, Pencil, SearchX } from "lucide-react";
import { cn } from "../../lib/utils";
import { statusLabel, statusTone } from "../../lib/workspace-file-status";
import { EmptyMessage, PanelMessage, RetryButton } from "../workspace-diff/DiffChrome";
import { FileAnnotationComposer, FileAnnotationSendBar } from "../workspace-diff/FileAnnotationComposer";
import { LineFeedbackButtonControl } from "../workspace-diff/LineFeedback";
import { VscodeGoToFileIcon } from "../icons/VscodeGoToFileIcon";
import { WorkspaceEntryIcon } from "../WorkspaceEntryIcon";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Popover, PopoverAnchor, PopoverContent } from "../ui/popover";
import { TALLPINE_PIERRE_FILES_REVIEW_CSS, TALLPINE_PIERRE_SURFACE_CSS } from "./pierreTheme";
import { SelectionAskButton } from "./SelectionAskButton";
import { useReviewPaneState } from "./workspace-review/useReviewPaneState";
import { useReviewPaneData } from "./workspace-review/useReviewPaneData";
import { useReviewPaneActions } from "./workspace-review/useReviewPaneActions";
import type { WorkspaceReviewPaneProps } from "./workspace-review/review-pane-props";
import { REVIEW_CODE_VIEW_LAYOUT, REVIEW_CODE_VIEW_METRICS, REVIEW_CODE_VIEW_THEME } from "./workspace-review/review-helpers";
import { HeaderActionTooltip } from "./workspace-review/HeaderActionTooltip";


export function WorkspaceReviewPane(props: WorkspaceReviewPaneProps) {
	const {
		annotation,
		target,
		onRetry,
		onBrowseAll,
		canOpenInCenter = true,
		onIncludeCommitsBetween,
		onShowAllCommits,
		split,
	} = props;
	const state = useReviewPaneState(props);
	const data = useReviewPaneData(props, state);
	const actions = useReviewPaneActions(props, state, data);
	const {
		allFiles,
		codeViewRef,
		collapsedPaths,
		fileMenuHandoffRef,
		files,
		gutterHover,
		onOpenFile,
		resolvedTheme,
		reviewRef,
		scope,
		t,
		viewed,
		workspaceVersion,
	} = state;
	const {
		codeSelection,
		items,
		loadDiffFiles,
		summaryById,
	} = data;
	const {
		beginLineAnnotation,
		conflict,
		emptyMessage,
		fallbackFiles,
		firstError,
		groupError,
		loading,
		markViewed,
		renderFallbackFiles,
		retryAll,
		toggleCollapsed,
		fileOpenContext,
	} = actions;

	return (
		<div
			className="@container/review flex h-full min-h-0 flex-col"
			onPointerLeave={gutterHover.onPointerLeave}
			onPointerMove={gutterHover.onPointerMove}
			ref={reviewRef}
		>
			{target.state === "pending" ? (
				<PanelMessage compact>{(target.commitShas?.length ?? 0) > 1 ? t("files.changes.combining") : t("files.loading")}</PanelMessage>
			) : target.state === "error" ? (
				<PanelMessage action={onRetry ? <RetryButton onClick={onRetry} /> : undefined}>{target.error?.message || t("files.error.loadWorkspace")}</PanelMessage>
			) : target.state === "not_combinable" ? (
				<PanelMessage
					action={(
						<div className="flex flex-wrap items-center justify-center gap-2">
							{onIncludeCommitsBetween ? <Button onClick={onIncludeCommitsBetween} size="sm" type="button" variant="outline">{t("files.changes.includeBetween")}</Button> : null}
							{onShowAllCommits ? <Button onClick={onShowAllCommits} size="sm" type="button" variant="outline">{t("files.changes.showAllCommits")}</Button> : null}
						</div>
					)}
				>
					{conflict?.reason === "unsupported_history"
						? t("files.changes.unsupportedHistory")
						: t("files.changes.notCombinable", { subject: conflict?.subject || conflict?.commitSha.slice(0, 7) || "" })}
				</PanelMessage>
			) : (
				<>
			{firstError ? <PanelMessage action={<RetryButton onClick={retryAll} />}>{firstError.message}</PanelMessage> : null}
			{groupError ? <PanelMessage action={<RetryButton onClick={retryAll} />}>{groupError.message}</PanelMessage> : null}
			{loading && items.length === 0 ? <PanelMessage compact>{t("files.loadingDiff")}</PanelMessage> : null}
			{files.length === 0 ? (
				<EmptyMessage
					action={allFiles.length === 0 && onBrowseAll ? <Button onClick={onBrowseAll} type="button">{t("files.browseAll")}</Button> : undefined}
					icon={allFiles.length === 0 ? GitCompareArrows : SearchX}
					title={allFiles.length === 0 ? emptyMessage : t("files.noFilterMatches")}
				/>
			) : null}
			<div className={cn("min-h-0 overflow-hidden", files.length > 0 && "flex-1")}>
				{items.length > 0 || fallbackFiles.length > 0 ? (
					<CodeView<"feedback">
						ref={codeViewRef}
						className="tallpine-pierre-surface board-scrollbar h-full min-h-0 select-text overflow-y-auto overscroll-contain"
						disableWorkerPool={typeof Worker === "undefined"}
						items={items}
						renderCodeViewFooter={renderFallbackFiles}
						options={{
							collapsedContextThreshold: 8,
							diffIndicators: "classic",
							diffStyle: split ? "split" : "unified",
							enableGutterUtility: true,
							expansionLineCount: 20,
							hunkSeparators: "line-info",
							lineDiffType: "word-alt",
							layout: REVIEW_CODE_VIEW_LAYOUT,
							// Must match the custom file header height (h-9 = 36px). Pierre's default
							// (44px) reserves the difference and pushes the header down by 8px.
							itemMetrics: REVIEW_CODE_VIEW_METRICS,
							lineHoverHighlight: "line",
							loadDiffFiles,
							maxLineDiffLength: 400,
							onPostRender: gutterHover.restoreAfterRender,
							overflow: "wrap",
							stickyHeaders: true,
							theme: REVIEW_CODE_VIEW_THEME,
							themeType: resolvedTheme,
							tokenizeMaxLength: 200_000,
							tokenizeMaxLineLength: 2_000,
							unsafeCSS: TALLPINE_PIERRE_SURFACE_CSS + TALLPINE_PIERRE_FILES_REVIEW_CSS,
						}}
						renderAnnotation={(line, item) => {
							const path = summaryById.get(item.id)?.path;
							const side = "side" in line ? line.side : undefined;
							const target = annotation.targets.find((open) => open.surface !== "focused" && open.path === path && open.side !== "file" && open.line === line.lineNumber && (open.side === "old" ? "deletions" : "additions") === side);
							return target ? <FileAnnotationComposer annotation={annotation} target={target} /> : null;
						}}
						renderGutterUtility={(getHoveredLine, item) => (
							<LineFeedbackButtonControl
								gutter
								label={t("files.addFeedback")}
								onClick={() => {
									const line = getHoveredLine();
									if (!line) return;
									const side = "side" in line ? line.side : undefined;
									if (side === "additions" || side === "deletions") beginLineAnnotation(item.id, line.lineNumber, side);
								}}
							/>
						)}
						renderCustomHeader={(item) => {
							const file = summaryById.get(item.id);
							if (!file) return null;
							const isViewed = viewed.has(file.path);
							const isCollapsed = collapsedPaths.has(file.path);

							const fileAnnotationTarget = annotation.targets.find((target) => target.surface !== "focused" && target.path === file.path && target.side === "file");
							const fileAnnotationActive = fileAnnotationTarget != null;
							return (
								<Popover onOpenChange={(open) => { if (!open && fileAnnotationTarget) annotation.cancel(fileAnnotationTarget); }} open={fileAnnotationActive}>
									{/* Lets a code selection in this file's surface find its item. */}
									<div className="relative bg-background" data-review-item-id={item.id}>
										{/* The whole row toggles the file (the chevron just rotates);
										    name + stats on the left, every action grouped on the right
										    with "viewed" pinned to the far edge. */}
										<PopoverAnchor asChild>
											<div className="group/file-header flex h-9 min-w-0 cursor-pointer items-center gap-2 pl-4 pr-1.5 hover:bg-interactive-hover/40" onClick={() => toggleCollapsed(file)}>
												<ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform duration-150 group-hover/file-header:text-foreground", !isCollapsed && "rotate-90")} />
												<WorkspaceEntryIcon className="size-icon-xl" kind="file" name={file.path.split("/").pop() ?? file.path} />
												<div className="flex min-w-0 shrink items-baseline gap-3">
													<button
														aria-expanded={!isCollapsed}
														aria-label={isCollapsed ? t("files.expandFile", { file: file.path }) : t("files.collapseFile", { file: file.path })}
														className="flex min-w-0 shrink items-baseline text-left text-[length:var(--font-size-base)] outline-none focus-visible:underline"
														title={file.path}
														type="button"
													>
														{file.path.includes("/") ? <span className="min-w-0 truncate text-muted-foreground">{file.path.slice(0, file.path.lastIndexOf("/") + 1)}</span> : null}
														<span className="max-w-full shrink-0 truncate text-foreground">{file.path.slice(file.path.lastIndexOf("/") + 1)}</span>
													</button>
													<span className="flex shrink-0 items-baseline gap-2.5 text-xs tabular-nums">
														<span className={cn("font-semibold", statusTone[file.status])}>{statusLabel[file.status]}</span>
														<span className="flex items-baseline gap-1.5 @max-[25rem]/review:hidden">
															<span className="text-success">+{file.additions}</span>
															<span className="text-error">−{file.deletions}</span>
														</span>
													</span>
												</div>
												{/* Same 4px gap as the inspector tab buttons. In a narrow pane the
												    secondary actions fold into one ⋮ menu (like the browser's
												    controls) and the extra lead-in goes, so the file name keeps its
												    room; the row's own gap still separates it from the stats. */}
												<div className="ml-auto flex shrink-0 items-center gap-1 pl-2 @max-[25rem]/review:pl-0" onClick={(event) => event.stopPropagation()}>
													<div className="flex items-center gap-1">
              {onOpenFile && target.source?.kind === "snapshot" ? <HeaderActionTooltip label={t("files.readOnlySnapshot")}><span className="@max-[25rem]/review:hidden" tabIndex={0}><Button aria-label={t("files.editFile")} disabled className="size-6" size="icon-sm" type="button" variant="ghost"><Pencil aria-hidden="true" className="size-icon-sm"/></Button></span></HeaderActionTooltip> : null}
														{onOpenFile && !target.source && file.editable && file.fileFingerprint ? (
															<HeaderActionTooltip label={t("files.editFile")}>
																<Button aria-label={t("files.editFile")} className="@max-[25rem]/review:hidden size-6 text-muted-foreground hover:text-foreground" onClick={() => onOpenFile(file.path, { editing: true, mode: "file", scope })} size="icon-sm" type="button" variant="ghost"><Pencil aria-hidden="true" className="size-icon-sm" /></Button>
															</HeaderActionTooltip>
														) : null}
														{onOpenFile ? (
															<HeaderActionTooltip label={t("files.openFullFileGeneric")}>
																<Button aria-label={t("files.openFullFileGeneric")} className="size-6 text-muted-foreground hover:text-foreground" onClick={() => onOpenFile(file.path, { ...fileOpenContext, mode: "file" })} size="icon-sm" type="button" variant="ghost"><FileCode2 aria-hidden="true" className="size-icon-sm" /></Button>
															</HeaderActionTooltip>
														) : null}
														{onOpenFile && canOpenInCenter ? (
															<HeaderActionTooltip label={t("files.openDiffInCenter")}>
																<Button aria-label={t("files.openDiffInCenter")} className="size-6 text-muted-foreground hover:text-foreground" onClick={() => onOpenFile(file.path, { ...fileOpenContext, mode: "diff", center:true })} size="icon-sm" type="button" variant="ghost"><VscodeGoToFileIcon aria-hidden="true" className="size-icon-sm" /></Button>
															</HeaderActionTooltip>
														) : null}
														<HeaderActionTooltip label={t("files.addFeedback")}>
															<Button aria-label={t("files.addFeedback")} aria-pressed={fileAnnotationActive} className={cn("@max-[25rem]/review:hidden size-6 text-muted-foreground hover:text-foreground", fileAnnotationActive && "bg-interactive-active text-foreground")} onClick={() => annotation.begin({ path: file.path, previousPath: file.previousPath, side: "file", scope, surface: "review", workspaceVersion, fileFingerprint: file.fileFingerprint })} size="icon-sm" type="button" variant="ghost"><MessageSquarePlus aria-hidden="true" className="size-icon-sm" /></Button>
														</HeaderActionTooltip>
													</div>
													<DropdownMenu>
														<HeaderActionTooltip label={t("files.moreFileActions")}>
															<DropdownMenuTrigger asChild>
																<Button aria-label={t("files.moreFileActions")} className="hidden size-6 text-muted-foreground hover:text-foreground data-[state=open]:bg-interactive-active data-[state=open]:text-foreground @max-[25rem]/review:inline-flex" size="icon-sm" type="button" variant="ghost"><MoreVertical aria-hidden="true" className="size-icon-sm" /></Button>
															</DropdownMenuTrigger>
														</HeaderActionTooltip>
														<DropdownMenuContent
															align="end"
															onCloseAutoFocus={(event) => {
																if (fileMenuHandoffRef.current) event.preventDefault();
																fileMenuHandoffRef.current = false;
															}}
														>
															{onOpenFile && !target.source && file.editable && file.fileFingerprint ? (
																<DropdownMenuItem className="gap-2" onSelect={() => { fileMenuHandoffRef.current = true; onOpenFile?.(file.path, { editing: true, mode: "file", scope }); }}>
																	<Pencil aria-hidden="true" className="size-icon-sm text-passive" />
																	{t("files.editFile")}
																</DropdownMenuItem>
															) : null}


															<DropdownMenuItem className="gap-2" onSelect={() => { fileMenuHandoffRef.current = true; annotation.begin({ path: file.path, previousPath: file.previousPath, side: "file", scope, surface: "review", workspaceVersion, fileFingerprint: file.fileFingerprint }); }}>
																<MessageSquarePlus aria-hidden="true" className="size-icon-sm text-passive" />
																{t("files.addFeedback")}
															</DropdownMenuItem>
														</DropdownMenuContent>
													</DropdownMenu>
													<HeaderActionTooltip label={isViewed ? t("files.markUnviewed", { file: file.path }) : t("files.markViewed", { file: file.path })}>
														{/* A 24px slot like the buttons beside it keeps the checkbox
														    centred on the header's trailing action column. */}
														<span className="grid size-6 place-items-center">
															<Checkbox
																aria-label={isViewed ? t("files.markUnviewed", { file: file.path }) : t("files.markViewed", { file: file.path })}
																checked={isViewed}
																className="size-4 border border-muted-foreground/70 bg-transparent data-[state=checked]:border-foreground data-[state=checked]:bg-foreground data-[state=checked]:text-on-foreground"
																onCheckedChange={(checked) => markViewed(file, checked === true)}
															/>
														</span>
													</HeaderActionTooltip>
												</div>
											</div>
										</PopoverAnchor>
									</div>
									{/* Whole-file feedback floats under the header's right edge, like the
									    browser's comment box: portaled above the list so no later file
									    header can cover it, and the same in split and unified views. It
									    stays open on outside clicks so a draft isn't lost; the close
									    button, Esc, or this file's feedback button close it. */}
									<PopoverContent
										align="end"
										aria-label={t("files.addFeedback")}
										className="w-[min(32rem,var(--radix-popover-trigger-width))] rounded-2xl border-0 bg-transparent p-0"
										hideWhenDetached
										onInteractOutside={(event) => event.preventDefault()}
										onOpenAutoFocus={(event) => event.preventDefault()}
										side="bottom"
										sideOffset={0}
									>
										{fileAnnotationTarget ? <FileAnnotationComposer annotation={annotation} target={fileAnnotationTarget} /> : null}
									</PopoverContent>
								</Popover>
							);
						}}
						style={{ height: "100%" }}
					/>
				) : null}
				<SelectionAskButton onAsk={codeSelection.ask} source={codeSelection.source} />
			</div>
				</>
			)}
			<FileAnnotationSendBar annotation={annotation} surface="review" />
		</div>
	);
}
