import { useMemo } from "react";
import { useSettings, useUpdateHarnessDefault } from "./useSettings";
import { useRestartAgent } from "./useRestartAgent";
import { effectiveAutoCompact, supportsAutoCompact } from "../lib/auto-compact";
import type { ContextDetails } from "../lib/context-view";
import type { ConversationCompaction, ConversationRateLimits, TurnSettings } from "../types/conversation";

interface ContextDetailsInput {
	sessionId?: string;
	hostId?: string;
	harness?: string;
	model?: string;
	/** The picker's name for the running model; shown when no model is saved. */
	modelLabel?: string;
	/** The session's saved choices; the compaction window is one of them. */
	settings?: TurnSettings;
	onChooseSettings?: (settings: TurnSettings) => void;
	compactedAt?: string;
	rateLimits?: ConversationRateLimits;
	lastCompaction?: ConversationCompaction;
}

/**
 * What the context popover needs for one session: the model, the effective
 * compaction choice (its own, else the Settings default for its agent), the
 * daemon-resolved capacity and threshold, the quota, and the control to change
 * the window for this session or for the agent.
 */
export function useContextDetails({ sessionId, hostId, harness, model, modelLabel, settings, onChooseSettings, compactedAt, rateLimits, lastCompaction }: ContextDetailsInput) {
	const { settings: appSettings } = useSettings(hostId);
	const { update: updateDefault, error: defaultError } = useUpdateHarnessDefault(hostId);
	const restart = useRestartAgent(sessionId, hostId);
	const support = supportsAutoCompact(harness, model, false);
	const harnessDefault = harness ? appSettings?.harnessDefaults?.[harness]?.autoCompactWindow : undefined;
	const own = settings?.autoCompactWindow;
	const compactsAt = support === "yes" ? effectiveAutoCompact(own, harnessDefault) : undefined;
	const pending = Boolean(settings?.autoCompactPending);
	const details = useMemo<ContextDetails>(() => {
		const canChange = support === "yes" && harness && onChooseSettings;
		return {
			harness,
			model: settings?.modelLabel || modelLabel || model,
			capacity: settings?.contextCapacity,
			compactThreshold: settings?.compactThreshold,
			autoCompact: compactsAt,
			projectTokens: settings?.autoCompactProjectTokens,
			// Only Claude Code chats can break the window down (Codex shows totals only).
			breakdown: sessionId && harness === "claude-code" ? { sessionId, hostId } : undefined,
			modelUsage: sessionId ? { sessionId, hostId } : undefined,
			autoCompactUnsupported: support === "unsupported",
			compactedAt,
			rateLimits,
			lastCompaction,
			compaction: canChange ? {
				value: own,
				defaultChoice: harnessDefault || undefined,
				pendingRestart: pending,
				onChange: (value) => {
					// The daemon replaces the saved choices as a whole; its pending flag is read-only.
					const { autoCompactPending: _pending, autoCompactProjectTokens: _project, contextCapacity: _capacity, compactThreshold: _threshold, modelLabel: _label, ...choices } = settings ?? {};
					onChooseSettings({ ...choices, autoCompactWindow: value });
				},
				onSetDefault: (value) => { void updateDefault({ agentId: harness, autoCompactWindow: value }).catch(() => undefined); },
				onRestart: sessionId ? restart.restart : undefined,
				restarting: restart.restarting,
				restartBusy: restart.busy,
				error: restart.error ?? defaultError,
			} : undefined,
		};
	}, [harness, model, modelLabel, hostId, compactsAt, support, settings?.autoCompactProjectTokens, settings?.modelLabel, settings?.contextCapacity, settings?.compactThreshold, compactedAt, rateLimits, lastCompaction, own, harnessDefault, pending, settings, onChooseSettings, sessionId, updateDefault, restart.restart, restart.restarting, restart.busy, restart.error, defaultError]);
	return { details, compactsAt };
}
