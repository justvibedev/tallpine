import { ChevronLeft, ChevronRight, CircleArrowUp, LoaderCircle, TriangleAlert } from "lucide-react";
import {
	isActiveInstallJob,
	versionLabel,
	} from "../../../hooks/useHarnessUpdates";
import { agentLabel } from "../../../lib/agent-options";
import { labelForHost } from "../../../lib/host-clients";
import { cn } from "../../../lib/utils";
import { AgentAvatar } from "../../AgentAvatar";
import { ConfirmDialog } from "../../ConfirmDialog";
import { Button } from "../../ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../../ui/tabs";
import { machineText } from "./harness-diagnostics";
import { HarnessHealthTab } from "./HarnessHealthTab";
import { HARNESS_TAB_CLASS, HARNESS_TABS_CLASS, HarnessStatusDot, type HarnessDetailTab } from "./harness-ui";
import { HarnessModelsPanel } from "./HarnessModelsPanel";
import { HarnessSessionsDialog } from "./HarnessSessionsDialog";
import { Spinner } from "../../ui/spinner";
import { useLocalHarnessState } from "./local-harness/useLocalHarnessState";
import { useLocalHarnessEffects } from "./local-harness/useLocalHarnessEffects";
import { useLocalHarnessOperations } from "./local-harness/useLocalHarnessOperations";
import { useLocalHarnessAuth } from "./local-harness/useLocalHarnessAuth";
import type { LocalHarnessContentProps } from "./local-harness/local-harness-types";

export function LocalHarnessContent(props: LocalHarnessContentProps) {
	const {
		hostId,
	} = props;
	const state = useLocalHarnessState(props);
	const effects = useLocalHarnessEffects(props, state);
	const ops = useLocalHarnessOperations(props, state, effects);
	const auth = useLocalHarnessAuth(props, state, effects, ops);
	const {
		agentAuthPlans,
		agents,
		authCheckErrors,
		authPlans,
		authStartPendingRef,
		authStates,
		authWorkflow,
		checkingAuth,
		client,
		closeDetail,
		copiedAgent,
		currentNoticeError,
		detailRef,
		detailTab,
		highlightedAgentId,
		inUseSessions,
		installers,
		jobMap,
		jobs,
		logoutRequest,
		needsAuthentication,
		openDetail,
		pendingAgentIds,
		queryClient,
		refreshInstalledAgent,
		rows,
		selectedAgentId,
		setDetailTab,
		setInUseSessions,
		setLogoutRequest,
		t,
		describe,
		healthReport,
	} = state;
	const {
		layoutRef,
		split,
	} = effects;
	const {
		copyText,
		startAuth,
	} = ops;
	const {
		accountTab,
		detailsOpen,
		selectedDetails,
	} = auth;

	return (
		<>
			{installers.error || authPlans.error || agents.error || jobs.error ? (
				<div className="flex items-center gap-2 rounded-md border border-error/30 bg-error/10 px-3 py-2 text-xs text-error">
					<TriangleAlert className="size-4" aria-hidden="true" />
					{jobs.error instanceof Error ? jobs.error.message : t("settings.harness.loadFailed")}
				</div>
			) : null}

			<div ref={layoutRef} data-harness-detail="" className={cn("relative flex w-full min-w-0 max-w-full flex-col overflow-hidden", split && "grid-rows-[minmax(0,1fr)]", split && "grid grid-cols-[minmax(14rem,16rem)_minmax(0,1fr)] items-stretch overflow-clip rounded-xl border border-border bg-(--color-bg-primary)")}>
				{split || !selectedDetails ? (
					<div className={cn("flex w-full flex-col", split
						? "relative divide-y divide-border bg-(--color-bg-secondary)"
						: "relative divide-y divide-border rounded-lg border border-border")}>
						{rows.map((agentId) => {
							const details = describe(agentId);
							const selected = split && selectedAgentId === agentId;
							const statusId = `harness-list-status-${agentId}`;
							return <button key={agentId}
								type="button"
								aria-label={t("settings.harness.openDetails", { agent: agentLabel(agentId) })}
								// The status mark is visual only; its words reach screen readers here.
								aria-describedby={statusId}
								aria-current={selected ? "true" : undefined}
								className={cn(
									"flex w-full items-center gap-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
									split ? "min-h-11 shrink-0 px-3" : "min-h-12 shrink-0 px-3",
									selected
										? (split ? "bg-(--color-bg-tertiary) font-medium" : "bg-accent font-medium")
										: "hover:bg-(--color-interactive-hover)",
								)}
								data-agent={agentId}
								onClick={() => openDetail(agentId)}
							>
								{/* sr-only is absolutely positioned: the list is `relative` so these stay inside its scroll box instead of stretching the app shell. */}
								<span id={statusId} className="sr-only">{details.statusLabel}</span>
								{/* Not-installed harnesses stay in place but recede. */}
								<AgentAvatar className={cn("shrink-0", split ? "size-5" : "size-6", !details.isInstalled && "opacity-45 grayscale")} decorative provider={agentId} />
								<span className={cn("min-w-0 flex-1 truncate text-sm", details.isInstalled || selected ? "text-settings-label" : "text-settings-muted")}>{agentLabel(agentId)}</span>
								{/* One status slot: progress replaces the update icon while an install, update or login runs. */}
								{details.busy ? <span role="status" className="flex size-4 shrink-0 items-center justify-center"><Spinner icon={LoaderCircle} strokeWidth={1.75} className="size-4 text-settings-muted" /><span className="sr-only">{details.progressLabel}</span></span>
									: details.authBusy ? <Spinner icon={LoaderCircle} strokeWidth={1.75} className="size-4 shrink-0 text-settings-muted" />
									: details.isInstalled && details.updateAvailable ? <CircleArrowUp
										className="size-4 shrink-0 text-settings-muted"
										strokeWidth={1.75}
										aria-label={t("settings.harness.versionAvailable", { version: versionLabel(details.advisory!.latestVersion!) })}
										role="img"
									/>
									: details.listTone === "error" ? <HarnessStatusDot tone="error" /> : null}
								{split ? null : <ChevronRight className="size-4 shrink-0 text-settings-muted" aria-hidden="true" />}
							</button>;
						})}
						{rows.length === 0 ? <p className="px-3 py-6 text-center text-sm text-settings-muted">{t("settings.harness.noResults")}</p> : null}
					</div>
				) : null}
				{selectedDetails ? (
					<div
						className={cn("flex w-full min-w-0 flex-col gap-4", detailsOpen && "min-h-0 flex-1 overflow-hidden", split && "p-5")}
						data-agent={selectedDetails.agentId}
						data-focus-highlighted={highlightedAgentId === selectedDetails.agentId ? "" : undefined}
						ref={detailRef}
					>
						{split ? null : <div className="shrink-0">
							<Button type="button" size="sm" variant="ghost" className="-ml-2 h-7 gap-1 px-2 text-settings-muted" onClick={closeDetail}>
								<ChevronLeft aria-hidden="true" />{t("settings.harness.allHarnesses")}
							</Button>
						</div>}
						<div
							className={cn("-mx-2 flex shrink-0 items-center gap-3 rounded-lg px-2 py-1 transition-[background-color,box-shadow] duration-200", highlightedAgentId === selectedDetails.agentId && "bg-accent-weak ring-2 ring-inset ring-accent")}
						>
							<AgentAvatar className="size-7 shrink-0" decorative provider={selectedDetails.agentId} />
							{/* Pane title: one step above row labels (15px), below the page heading. */}
							<span className="min-w-0 flex-1 text-subtitle font-semibold leading-6 text-settings-label" id={`harness-agent-${selectedDetails.agentId}`}>{agentLabel(selectedDetails.agentId)}</span>
						</div>
						<Tabs className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden" value={detailTab} onValueChange={(value) => setDetailTab(value as HarnessDetailTab)}>
							<TabsList className={HARNESS_TABS_CLASS} aria-label={t("settings.harness.detailTabs", { agent: agentLabel(selectedDetails.agentId) })}>
								<TabsTrigger className={HARNESS_TAB_CLASS} value="account">{t("settings.harness.tabAccount")}</TabsTrigger>
								<TabsTrigger className={HARNESS_TAB_CLASS} value="models">{t("settings.harness.tabModels")}</TabsTrigger>
								<TabsTrigger className={HARNESS_TAB_CLASS} value="health">{t("settings.harness.tabHealth")}</TabsTrigger>
							</TabsList>
							<TabsContent value="account" className="settings-thin-scrollbar min-h-0 min-w-0 flex-1 overflow-x-hidden pt-4">{accountTab(selectedDetails)}</TabsContent>
							<TabsContent value="models" className="min-h-0 min-w-0 overflow-hidden pt-4 data-[state=active]:flex data-[state=active]:flex-col">
								<HarnessModelsPanel
									agentId={selectedDetails.agentId}
									hostId={hostId}
									installed={selectedDetails.isInstalled}
									needsLogin={needsAuthentication(selectedDetails.agentId)}
									onOpenAccount={() => setDetailTab("account")}
								/>
							</TabsContent>
							<TabsContent value="health" className="settings-thin-scrollbar min-h-0 min-w-0 flex-1 overflow-x-hidden pt-4">
								<HarnessHealthTab
									details={selectedDetails}
									checking={checkingAuth.includes(selectedDetails.agentId) || agents.isFetching || Boolean(selectedDetails.advisoryQuery?.isFetching)}
									authCheckFailed={Boolean(authCheckErrors[selectedDetails.agentId])}
									updateError={currentNoticeError(selectedDetails.agentId)}
									copied={copiedAgent === selectedDetails.agentId}
									onCheckAgain={() => refreshInstalledAgent(selectedDetails.agentId)}
									onCopyReport={() => void (async () => {
										const machine = await machineText(queryClient, hostId ? client : undefined);
										const noticeFailure = currentNoticeError(selectedDetails.agentId);
										const updateError = noticeFailure ? `Update error: ${noticeFailure}` : "";
										await copyText(selectedDetails.agentId, [updateError, healthReport(selectedDetails), machine].filter(Boolean).join("\n\n"));
									})()}
								/>
							</TabsContent>
						</Tabs>
					</div>
				) : null}
			</div>
			<ConfirmDialog
				open={logoutRequest !== null}
				title={t("settings.harness.logoutConfirmTitle", { agent: logoutRequest ? agentLabel(logoutRequest) : "" })}
				description={t("settings.harness.logoutConfirmDescription", { host: hostId ? labelForHost(hostId) ?? hostId : t("settings.harness.thisComputer") })}
				confirmLabel={t("settings.harness.logout")}
				destructive
				busy={logoutRequest ? authStates[logoutRequest]?.pending : false}
				confirmDisabled={!logoutRequest || !agentAuthPlans.get(logoutRequest)?.available || !agentAuthPlans.get(logoutRequest)?.logoutCommand || Boolean(authWorkflow) || (logoutRequest ? pendingAgentIds.has(logoutRequest) || isActiveInstallJob(jobMap.get(logoutRequest)) : false)}
				error={logoutRequest ? authStates[logoutRequest]?.error : null}
				onConfirm={() => { if (logoutRequest) void startAuth(logoutRequest, "logout").then((started) => { if (started) setLogoutRequest(null); }); }}
				onOpenChange={(open) => { if (!open && !authStartPendingRef.current) setLogoutRequest(null); }}
			/>
			<HarnessSessionsDialog
				agentId={inUseSessions?.agentId ?? null}
				inUse={inUseSessions?.inUse}
				hostId={hostId}
				onOpenChange={(open) => { if (!open) setInUseSessions(null); }}
			/>
		</>
	);
}
