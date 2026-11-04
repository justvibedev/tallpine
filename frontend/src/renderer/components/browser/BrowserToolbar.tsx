import { createPortal } from "react-dom";
import { ArrowLeft, ArrowRight, Bug, Camera, Check, ChevronRight, Download, Maximize2, Minimize2, RotateCcw, Monitor, MoreVertical, MousePointer2, RefreshCw, Settings2, Smartphone, Tablet, UserRound, X } from "lucide-react";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Input } from "../ui/input";
import { cn } from "../../lib/utils";
import { BrowserDownloadsList } from "../BrowserDownloadsList";
import type { BrowserPanelViewProps, BrowserPanelViewState } from "./browser-types";
import { BrowserControlTooltip } from "./BrowserTabs";
import { DEVICE_PRESETS, CUSTOM_DEVICE_PRESET_ID, MAX_DEVICE_FRAME_WIDTH, MIN_DEVICE_FRAME_WIDTH } from "./browser-constants";

/** Toolbar row: navigation, address field, new tab, annotate and menu. */
export function BrowserToolbar({ props, v }: { props: BrowserPanelViewProps; v: BrowserPanelViewState }) {
	const {
		poppedOut,
		onTogglePopOut,
		topbarHost,
	} = props;
	const {
		activeTabId,
		agentBrowserActive,
		annotationMode,
		browserDownloads,
		browserProfiles,
		canAnnotate,
		canRetryAnnotation,
		canTakeScreenshot,
		canUseDevTools,
		closeDevTools,
		closedTabs,
		controlsOpen,
		controlsView,
		customDeviceWidth,
		customWidthInputRef,
		devicePreset,
		downloadsOpen,
		goBack,
		goForward,
		hasActiveDownload,
		navState,
		onAgentTab,
		openDevTools,
		openGlobalSettings,
		profilesLoading,
		reload,
		reopenClosedTab,
		runtimeReconnectPending,
		selectBrowserProfile,
		setControlsOpen,
		setControlsView,
		setCustomDeviceWidth,
		setDevicePreset,
		setDownloadsOpen,
		status,
		stop,
		t,
		tabNotice,
		agentStatusLabel,
		annotationStatusLabel,
		handleCloseTab,
		reconnectRuntime,
		runtimeDisconnected,
		runtimeRejectedMessage,
		runtimeState,
		takeScreenshot,
		toggleAnnotationMode,
		browserAddressBar,
		newTabButton,
		devtoolsState,
		profileState,
	} = v;
	return (
		<>
			{/* One toolbar row: nav, address field, new tab, annotate, menu. The tab strip below it exists only with 2+ tabs. */}
			<div
				className="browser-panel__tab-row"
				data-testid="browser-tab-row"
			>
				<div className="browser-panel__toolbar" data-testid="browser-toolbar">
							{runtimeDisconnected ? (
								<div className="flex min-w-0 items-center gap-1.5 text-2xs text-destructive" data-testid="browser-runtime-alert" role="alert">
									<span className="truncate" title={runtimeRejectedMessage ?? undefined}>
										{runtimeRejectedMessage ?? t("browser.runtimeDisconnected")}
									</span>
									{/* Reconnecting sends the same token, which the daemon would refuse again. */}
									{runtimeState?.rejected ? null : (
										<Button
											className="shrink-0"
											disabled={runtimeReconnectPending}
											onClick={() => void reconnectRuntime()}
											size="sm"
											type="button"
											variant="outline"
										>
											{t("browser.reconnectRuntime")}
										</Button>
									)}
								</div>
							) : null}
							<BrowserControlTooltip label={t("browser.back")}>
								<span className="browser-panel__navigation-control inline-flex">
							<Button
								aria-label={t("browser.back")}
								className="browser-panel__navigation-btn"
								disabled={!navState.canGoBack}
								onClick={() => void goBack()}
								size="icon-sm"
								type="button"
								variant="ghost"
							>
								<ArrowLeft aria-hidden="true" className="size-icon-base" />
							</Button>
					</span>
				</BrowserControlTooltip>
				<BrowserControlTooltip label={t("browser.forward")}>
					<span className="browser-panel__navigation-control inline-flex">
							<Button
								aria-label={t("browser.forward")}
								className="browser-panel__navigation-btn"
								disabled={!navState.canGoForward}
								onClick={() => void goForward()}
								size="icon-sm"
								type="button"
								variant="ghost"
							>
								<ArrowRight aria-hidden="true" className="size-icon-base" />
							</Button>
					</span>
				</BrowserControlTooltip>
				<BrowserControlTooltip label={navState.isLoading ? t("browser.stop") : t("browser.reload")}>
					<Button
							aria-label={navState.isLoading ? t("browser.stop") : t("browser.reload")}
							className="browser-panel__navigation-btn"
							onClick={() => void (navState.isLoading ? stop() : reload())}
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							{navState.isLoading ? (
								<X aria-hidden="true" className="size-icon-base" />
							) : (
								<RefreshCw aria-hidden="true" className="size-icon-base" />
							)}
					</Button>
				</BrowserControlTooltip>
				{topbarHost ? createPortal(browserAddressBar, topbarHost) : browserAddressBar}
				{newTabButton}
				{annotationStatusLabel ? (
					<span className="sr-only" role="status">
						{annotationStatusLabel}
					</span>
				) : agentStatusLabel ? (
					<span aria-live="polite" className="sr-only" role="status">
						{agentStatusLabel}
					</span>
				) : null}
				{tabNotice ? (
					<span className="max-w-24 truncate text-caption text-accent" role="status">
						{tabNotice}
					</span>
				) : null}
				<BrowserControlTooltip
					label={
						annotationStatusLabel ||
						agentStatusLabel ||
						(canRetryAnnotation
							? t("browser.retryAnnotation")
							: onAgentTab
								? t("browser.annotateAgentTab")
								: t("browser.annotate"))
					}
				>
					<span className="inline-flex">
							<Button
								aria-label={
									canRetryAnnotation
										? t("browser.retryAnnotation")
										: annotationMode || status === "picking"
											? t("browser.cancelAnnotation")
											: t("browser.annotate")
								}
								aria-pressed={annotationMode || status === "picking"}
								className="browser-panel__annotate-btn relative"
								disabled={!canAnnotate || status === "sending"}
								onClick={() => void toggleAnnotationMode()}
								size="icon-sm"
								type="button"
								variant="ghost"
							>
								<MousePointer2 aria-hidden="true" className="size-icon-base" />
								{annotationStatusLabel ? (
									<span
										aria-hidden="true"
										className={cn(
											"pointer-events-none absolute -right-0.5 -top-0.5 size-1.5 rounded-full",
											status === "error" ? "bg-destructive" : "bg-status-needs-you-dot",
										)}
									/>
								) : agentStatusLabel ? (
									<span aria-hidden="true" className="pointer-events-none absolute -right-0.5 -top-0.5 size-1.5 rounded-full bg-accent" />
								) : null}
							</Button>
					</span>
				</BrowserControlTooltip>
				{browserDownloads.downloads.length > 0 ? (
					<DropdownMenu
						onOpenChange={(open) => {
							setDownloadsOpen(open);
						}}
						open={downloadsOpen}
					>
						<BrowserControlTooltip disabled={downloadsOpen} label={t("browser.downloads.title")}>
							<DropdownMenuTrigger asChild>
									<Button
									aria-label={t("browser.downloads.title")}
									className={cn("relative", hasActiveDownload && "text-accent")}
										size="icon-sm"
										type="button"
										variant="ghost"
									>
										<Download aria-hidden="true" className="size-icon-base" />
										{hasActiveDownload ? <span aria-hidden="true" className="absolute right-0.5 top-0.5 size-1.5 rounded-full bg-accent" /> : null}
									</Button>
							</DropdownMenuTrigger>
						</BrowserControlTooltip>
						<DropdownMenuContent
							align="end"
							className="w-96 p-0"
							data-browser-native-overlay="true"
						>
							<div className="flex items-center justify-between border-b border-border px-3 py-2">
								<p className="text-xs font-semibold">{t("browser.downloads.title")}</p>
								<Button onClick={() => openGlobalSettings("browserProfiles")} size="sm" type="button" variant="ghost">
									{t("browser.downloads.showAll")}
								</Button>
							</div>
							<BrowserDownloadsList
								compact
								downloads={browserDownloads.downloads.slice(0, 5)}
								error={browserDownloads.error}
								onAction={(id, action) => void browserDownloads.action(id, action)}
							/>
						</DropdownMenuContent>
					</DropdownMenu>
				) : null}
				{poppedOut ? (
					<BrowserControlTooltip label={t("browser.returnToPanel")}>
						<Button
							aria-label={t("browser.returnToPanel")}
							onClick={() => onTogglePopOut(false)}
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							<Minimize2 aria-hidden="true" className="size-icon-base" />
						</Button>
					</BrowserControlTooltip>
				) : null}
				<DropdownMenu
					onOpenChange={(open) => {
						setControlsOpen(open);
						if (!open) setControlsView("root");
					}}
				>
					<BrowserControlTooltip disabled={controlsOpen} label={t("browser.controls")}>
						<DropdownMenuTrigger asChild>
								<Button
									aria-label={t("browser.controls")}
									size="icon-sm"
									type="button"
									variant="ghost"
								>
									<MoreVertical aria-hidden="true" className="size-icon-base" />
								</Button>
						</DropdownMenuTrigger>
					</BrowserControlTooltip>
					<DropdownMenuContent
						align="end"
						className={controlsView === "root" ? "w-56" : "w-64"}
						data-browser-native-overlay="true"
					>
						{controlsView === "devices" ? (
							<>
								<DropdownMenuItem
									className="gap-1.5"
									onSelect={(event) => {
										event.preventDefault();
										setControlsView("root");
									}}
								>
									<ChevronRight aria-hidden="true" className="size-3.5 rotate-180 text-passive" />
									{t("browser.devicePreset")}
								</DropdownMenuItem>
								<div className="my-1 h-px bg-border" role="separator" />
						<DropdownMenuItem className="gap-1.5" onSelect={() => setDevicePreset(null)}>
							<span className="flex size-4 shrink-0 items-center justify-center">
								{devicePreset === null ? <Check aria-hidden="true" className="text-accent" /> : null}
							</span>
							{t("browser.deviceFit")}
						</DropdownMenuItem>
						<div className="my-1 h-px bg-border" role="separator" />
						<div className="board-scrollbar flex max-h-72 flex-col gap-px overflow-y-auto pr-0.5">
							{DEVICE_PRESETS.map((preset) => {
								const PresetIcon = preset.category === "tablet" ? Tablet : Smartphone;
								return (
									<DropdownMenuItem
										className="gap-1.5"
										key={preset.id}
										onSelect={() => setDevicePreset(preset.id)}
									>
										<span className="flex size-4 shrink-0 items-center justify-center">
											{devicePreset === preset.id ? <Check aria-hidden="true" className="text-accent" /> : null}
										</span>
										<PresetIcon aria-hidden="true" className="size-3.5 shrink-0 text-passive" />
										<span className="flex-1 truncate">{preset.label}</span>
										<span className="shrink-0 font-mono text-caption text-passive">
											{preset.width}×{preset.height}
										</span>
									</DropdownMenuItem>
								);
							})}
						</div>
						<div className="my-1 h-px bg-border" role="separator" />
						<DropdownMenuItem
							className="gap-1.5"
							// Radix re-focuses an item on pointer move/leave, which would pull focus
							// out of the width field while the mouse drifts over the row.
							onPointerMove={(event) => event.preventDefault()}
							onPointerLeave={(event) => event.preventDefault()}
							onSelect={(event) => {
								event.preventDefault();
								customWidthInputRef.current?.focus();
							}}
						>
							<span className="flex size-4 shrink-0 items-center justify-center">
								{devicePreset === CUSTOM_DEVICE_PRESET_ID ? <Check aria-hidden="true" className="text-accent" /> : null}
							</span>
							<span className="flex-1">{t("browser.deviceCustomWidth")}</span>
							<Input
								aria-label={t("browser.deviceCustomWidth")}
								ref={customWidthInputRef}
								className="h-6 w-16 shrink-0 px-1.5 text-right font-mono text-caption"
								inputMode="numeric"
								max={MAX_DEVICE_FRAME_WIDTH}
								min={MIN_DEVICE_FRAME_WIDTH}
								onChange={(event) => {
									setCustomDeviceWidth(event.target.value);
									setDevicePreset(CUSTOM_DEVICE_PRESET_ID);
								}}
								onClick={(event) => event.stopPropagation()}
								onKeyDown={(event) => {
									// Escape still closes the menu; everything else is typing.
									if (event.key !== "Escape") event.stopPropagation();
								}}
								type="number"
								value={customDeviceWidth}
							/>
						</DropdownMenuItem>
							</>
						) : controlsView === "profiles" ? (
							<>
								<DropdownMenuItem
									className="gap-1.5"
									onSelect={(event) => {
										event.preventDefault();
										setControlsView("root");
									}}
								>
									<ChevronRight aria-hidden="true" className="size-3.5 rotate-180 text-passive" />
									{t("browser.profile.label")}
								</DropdownMenuItem>
								<div className="my-1 h-px bg-border" role="separator" />
								<DropdownMenuItem className="gap-2" disabled={agentBrowserActive} onSelect={() => selectBrowserProfile(null)}>
									<span className="flex size-4 shrink-0 items-center justify-center">
										{profileState.profileId === null ? <Check aria-hidden="true" className="text-accent" /> : null}
									</span>
									<span className="flex-1 truncate">{t("browser.profile.temporary")}</span>
								</DropdownMenuItem>
								{profilesLoading ? (
									<div className="px-8 py-1.5 text-caption text-passive">{t("settings.browserProfiles.loading")}</div>
								) : (
									browserProfiles.map((profile) => (
										<DropdownMenuItem
											className="gap-2"
											disabled={agentBrowserActive}
											key={profile.id}
											onSelect={() => selectBrowserProfile(profile.id)}
										>
											<span className="flex size-4 shrink-0 items-center justify-center">
												{profileState.profileId === profile.id ? <Check aria-hidden="true" className="text-accent" /> : null}
											</span>
											<span className="flex-1 truncate">{profile.name}</span>
										</DropdownMenuItem>
									))
								)}
								<div className="my-1 h-px bg-border" role="separator" />
								<DropdownMenuItem onSelect={() => openGlobalSettings("browserProfiles")}>
									<Settings2 aria-hidden="true" className="size-icon-base" />
									{t("browser.profile.manage")}
								</DropdownMenuItem>
							</>
						) : (
							<>
								{!poppedOut ? (
									<>
										<DropdownMenuItem
											className="gap-2"
											onSelect={() => onTogglePopOut(true)}
										>
										<Maximize2 aria-hidden="true" className="size-icon-base shrink-0" />
											<span className="flex-1">{t("browser.popOut")}</span>
										</DropdownMenuItem>
										<div className="my-1 h-px bg-border" role="separator" />
									</>
								) : null}
								<DropdownMenuItem
									className="gap-2"
									onSelect={(event) => {
										event.preventDefault();
										setControlsView("devices");
									}}
								>
									<Monitor aria-hidden="true" className="size-icon-base shrink-0" />
									<span className="flex-1">{t("browser.devicePreset")}</span>
									{devicePreset !== null ? <span className="size-1.5 rounded-full bg-accent" /> : null}
									<ChevronRight aria-hidden="true" className="size-3.5 shrink-0 text-passive" />
								</DropdownMenuItem>
								<DropdownMenuItem
									className="gap-2"
									onSelect={(event) => {
										event.preventDefault();
										setControlsView("profiles");
									}}
								>
									<UserRound aria-hidden="true" className="size-icon-base shrink-0" />
									<span className="flex-1">{t("browser.profile.label")}</span>
									<span className="max-w-20 truncate text-caption text-passive">
										{profileState.profileName ?? t("browser.profile.temporary")}
									</span>
									<ChevronRight aria-hidden="true" className="size-3.5 shrink-0 text-passive" />
								</DropdownMenuItem>
								<DropdownMenuItem
									className="gap-2"
									disabled={!canUseDevTools}
									onSelect={() => void (devtoolsState.open ? closeDevTools() : openDevTools())}
								>
									<Bug aria-hidden="true" className="size-icon-base shrink-0" />
									<span className="flex-1">{t(devtoolsState.open ? "browser.closeDevTools" : "browser.openDevTools")}</span>
									{devtoolsState.open ? <Check aria-hidden="true" className="text-accent" /> : null}
								</DropdownMenuItem>
								<DropdownMenuItem className="gap-2" disabled={!canTakeScreenshot} onSelect={() => void takeScreenshot()}>
									<Camera aria-hidden="true" className="size-icon-base shrink-0" />
									<span className="flex-1">{t("browser.takeScreenshot")}</span>
								</DropdownMenuItem>
								<DropdownMenuItem className="gap-2" onSelect={() => openGlobalSettings("browserProfiles")}>
									<Download aria-hidden="true" className="size-icon-base shrink-0" />
									<span className="flex-1">{t("browser.downloads.title")}</span>
								</DropdownMenuItem>
								{activeTabId ? (
									<DropdownMenuItem className="gap-2" onSelect={() => handleCloseTab(activeTabId)}>
										<X aria-hidden="true" className="size-icon-base shrink-0" />
										<span className="flex-1">{t("browser.closeActiveTab")}</span>
									</DropdownMenuItem>
								) : null}
								{closedTabs.length > 0 ? (
									<DropdownMenuItem className="gap-2" onSelect={() => void reopenClosedTab()}>
										<RotateCcw aria-hidden="true" className="size-icon-base shrink-0" />
										<span className="flex-1">{t("browser.reopenClosedTab")}</span>
									</DropdownMenuItem>
								) : null}
							</>
						)}
					</DropdownMenuContent>
				</DropdownMenu>
				</div>
			</div>
		</>
	);
}
