/** Constants, ranks and props for the local harness settings content. */
import { probeAgentAuth } from "../../../../hooks/useAgentAuth";
import { type AgentId } from "../../../../lib/agent-options";
import type { HarnessDiagnosticsRequest } from "../../../../stores/settings-modal";
import { type AgentAuthState } from "../describe-harness";

export const AUTH_TERMINAL_LIFETIME_MS = 15 * 60_000;
// The first check right after a login terminal exits can fail transiently (the
// daemon's own readiness retry succeeds seconds later), so a login is re-checked
// a few times before the panel reports it did not take.
export const AUTH_VERIFY_ATTEMPTS = 4;
export const AUTH_VERIFY_RETRY_MS = 1_500;

export type AgentAuthStates = Partial<Record<AgentId, AgentAuthState>>;
export type AgentAuthProbeResult = Awaited<ReturnType<typeof probeAgentAuth>>;
export const AUTH_STATE_RANK = {
	authorized: 0,
	not_applicable: 0,
	unauthorized: 1,
	configured: 2,
	unknown: 2,
} as const;
export const INSTALL_STATE_RANK = {
	installed: 0,
	unknown: 1,
	not_installed: 2,
} as const;

export type LocalHarnessContentProps = {
	focusAgentId?: string;
	hostId?: string;
	search: string;
	startLogin?: boolean;
	showDiagnostics?: HarnessDiagnosticsRequest;
	onDetailChange?: (open: boolean) => void
};
