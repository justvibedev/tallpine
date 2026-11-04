import { useContextDetails } from "../../hooks/useContextDetails";
import type { ConversationCompaction, ConversationRateLimits, ConversationUsage, TurnSettings } from "../../types/conversation";
import { ContextMeter } from "./ContextMeter";

/**
 * The composer's context circle for one session: adds what the click-through
 * popover needs, namely the model, where this session compacts (its own choice,
 * else the Settings default for its agent), the quota the provider reported, and
 * the control to change the window for this session or for the agent.
 */
export function SessionContextMeter({
	usage,
	sessionId,
	hostId,
	harness,
	model,
	modelLabel,
	settings,
	onChooseSettings,
	compactedAt,
	rateLimits,
	lastCompaction,
}: {
	usage?: ConversationUsage;
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
}) {
	const { details, compactsAt } = useContextDetails({ sessionId, hostId, harness, model, modelLabel, settings, onChooseSettings, compactedAt, rateLimits, lastCompaction });
	return <ContextMeter usage={usage} compactsAt={compactsAt} details={details} />;
}
