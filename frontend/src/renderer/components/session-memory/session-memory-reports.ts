/** Copyable text reports for session and app memory. */
import { PRODUCT_NAME } from "../../../shared/brand";
import type { TFunction } from "i18next";
import { processCategories, processKind } from "@tallpine/product-ui";
import { formatEstimatedCost } from "../../lib/format-cost";
import { formatDurationClock as formatDuration } from "@tallpine/shared/format/duration";
import { formatTokenCount } from "../../lib/format-token-count";
import { formatCPU, formatMemory, type AppMemoryReading, type ReviewerMemoryReading, type SessionMemoryReading, type SystemMemoryReading } from "../../hooks/useSessionMemory";
import { type SessionUsageSummary } from "../../hooks/useSessionUsageSummaries";
import { type WorkspaceSession, type WorkspaceSummary } from "../../types/workspace";
import { statusText } from "./SessionMemoryRows";
import { processTree } from "./ProcessRows";

/**
 * One session as plain text: who it is, what it is doing,
 * what it costs the machine, and what it has been running. Process IDs are
 * deliberately left out — they mean nothing to whoever reads the report —
 * and so are tool arguments, which can carry paths and prompts.
 */
export function sessionReport(
	session: WorkspaceSession,
	reading: SessionMemoryReading,
	usage: SessionUsageSummary | undefined,
	t: TFunction,
): string {
	const working = session.activity?.state === "active";
	const identity = [session.workspaceName, session.kind ?? "worker", session.provider, session.mode].filter(Boolean).join(" · ");
	const lines = [session.title, identity];
	// The daemon's lane ("Needs review") and what the agent is doing right now
	// ("Bash · 38s") are different facts, but on an idle session they collapse
	// to the same word; print it once.
	const lane = session.displayStatus ?? session.status;
	const doing = statusText(reading.activity?.current, session, working, t);
	lines.push(`Status   ${doing.toLowerCase().startsWith(lane.toLowerCase()) ? doing : `${lane} · ${doing}`}`);
	if (session.branch) lines.push(`Branch   ${session.branch}`);
	for (const pr of session.prs ?? []) {
		lines.push(`PR       #${pr.number} · ${pr.state}${pr.ci ? ` · CI ${pr.ci}` : ""}${pr.review ? ` · ${pr.review}` : ""}`);
		lines.push(`         ${pr.url}`);
	}
	const cost = formatEstimatedCost(usage?.estimatedCost);
	const tokens = usage?.processedTokens != null ? formatTokenCount(usage.processedTokens) : undefined;
	if (cost || tokens) lines.push(`Usage    ${[cost, tokens].filter(Boolean).join(" · ")}`);
	lines.push(`Memory   ${formatMemory(reading.rssBytes)} · CPU ${formatCPU(reading.cpuPercent)} · ${reading.sampledAt}`);

	const tree = processTree(reading.processes);
	if (tree.length > 0) {
		const names = tree.map(({ process, depth }) => `${"  ".repeat(depth)}${processKind(process.command)}`);
		const width = Math.max(...names.map((name) => name.length));
		lines.push("", t("shell.memoryGroupProcesses"));
		const categories = processCategories(reading.processes);
		tree.forEach(({ process }, i) => {
			const category = categories.get(process.pid);
			const type = category ? `  ${t(`shell.memoryCategory.${category}`)}` : "";
			lines.push(`  ${names[i].padEnd(width)}  ${formatMemory(process.rssBytes).padStart(8)}  ${formatCPU(process.cpuPercent).padStart(4)}${type}`);
		});
	}
	const recent = reading.activity?.recent ?? [];
	if (recent.length > 0) {
		lines.push("", t("shell.memoryRecent"));
		for (const step of recent) {
			const started = new Date(step.startedAt);
			const duration = step.endedAt ? formatDuration(Date.parse(step.endedAt) - started.getTime()) : "";
			lines.push(
				`  ${started.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}  ${step.tool.padEnd(12)} ${duration}${step.failed ? ` ${t("shell.memoryStepFailed")}` : ""}`.trimEnd(),
			);
		}
	}
	return `${lines.join("\n")}\n`;
}

/** Every session's title by id, terminated ones included: a reviewer can
 * outlive the worker it reviews. */
export function sessionTitles(workspaces: readonly WorkspaceSummary[]): Map<string, string> {
	return new Map(workspaces.flatMap((workspace) => workspace.sessions.map((session) => [session.id, session.title] as const)));
}

/** A reviewer is named by the session it reviews; the id stands in once that session is gone. */
export function reviewerLabel(reviewer: ReviewerMemoryReading, titles: Map<string, string>, t: TFunction): string {
	return t("shell.memoryReviewerRow", { session: titles.get(reviewer.sessionId) ?? reviewer.sessionId });
}

/** One row of the report: a session with its reading, in the order shown. */
export type ReportRow = { session: WorkspaceSession; reading: SessionMemoryReading; usage?: SessionUsageSummary };

/**
 * Everything the window shows, as plain text for a bug report: the machine,
 * then every session in the order they are listed. One button copies the lot
 * — a reader needs the neighbours to judge whether one session is the
 * problem or the machine is simply full.
 */
export function diagnosticsReport(
	machine: { app?: AppMemoryReading; system?: SystemMemoryReading },
	rows: ReportRow[],
	t: TFunction,
	titles: Map<string, string>,
): string {
	const { app, system } = machine;
	const lines = [t("shell.memorySectionMachine")];
	if (app && system) {
		lines.push(`Memory   ${PRODUCT_NAME} ${formatMemory(app.rssBytes)} · ${formatMemory(system.availableBytes)} free of ${formatMemory(system.totalBytes)}`);
	} else if (app) {
		lines.push(`Memory   ${PRODUCT_NAME} ${formatMemory(app.rssBytes)}`);
	}
	if (system) {
		// A negative load average is Windows' "not applicable" sentinel, never a real reading.
		const load = system.load1 >= 0 ? ` · load ${system.load1.toFixed(2)}` : "";
		lines.push(`CPU      ${formatCPU(system.cpuPercent)} of ${system.cpuCount} cores · ${PRODUCT_NAME} ${formatCPU(Math.min(100, (app?.cpuPercent ?? 0) / Math.max(1, system.cpuCount)))}${load}`);
		if (system.swapBytesPerSec > 0) lines.push(`Swapping ${formatMemory(system.swapBytesPerSec)}/s`);
	}
	lines.push(`Sessions ${rows.length}`);
	return [`${lines.join("\n")}\n`, ...rows.map((row) => sessionReport(row.session, row.reading, row.usage, t)), ...tallpineReport(app, titles, t)].join("\n");
}

/** What Tallpine holds besides the sessions, so the report's rows add up to its total
 * the same way the window's do: each reviewer pane, then the daemon and app. */
export function tallpineReport(app: AppMemoryReading | undefined, titles: Map<string, string>, t: TFunction): string[] {
	if (!app) return [];
	const line = (label: string, reading: SessionMemoryReading) => `${label}\nMemory   ${formatMemory(reading.rssBytes)} · CPU ${formatCPU(reading.cpuPercent)}\n`;
	const reviewers = [...(app.reviewers ?? [])].sort((a, b) => b.memory.rssBytes - a.memory.rssBytes);
	return [
		...reviewers.map((reviewer) => line(`${reviewerLabel(reviewer, titles, t)} · ${reviewer.harness}`, reviewer.memory)),
		...(app.own ? [line(t("shell.memoryOwnRow"), app.own)] : []),
	];
}
