/**
 * Timeline entries for the Chat surface.
 *
 * One component per durable item shape, keyed by sequence so a streaming rewrite
 * updates a message in place instead of remounting it. Every component is a pure
 * function of a domain item: no lifecycle decisions, no capability checks, no
 * re-sorting. Those belong to the daemon.
 */

export { ActivityRow, ActivityTransition } from "./ChatTimelineItems/ActivityRow";
export { ApprovalCard } from "./ChatTimelineItems/ApprovalCard";
export { AssistantMessage, LiveResponseStatus, ResponseSpinner } from "./ChatTimelineItems/AssistantMessage";
export { providerErrorCopy } from "./ChatTimelineItems/ErrorActivityRow";
export { FileChangeList } from "./ChatTimelineItems/FileChangeList";
export { HumanMessage, humanBubbleClass, humanVisibleText } from "./ChatTimelineItems/HumanMessage";
export { MessageTimestamp, QueuedNote } from "./ChatTimelineItems/MessageParts";
export { OriginMessage } from "./ChatTimelineItems/OriginMessage";
export { SteerMessage, stripSteerSenderPrefix } from "./ChatTimelineItems/SteerMessage";
export { TurnChangedFiles } from "./ChatTimelineItems/TurnChangedFiles";
export { TurnDuration, TurnOutcome } from "./ChatTimelineItems/TurnOutcome";
export type { TurnOutcomeRetryControl } from "./ChatTimelineItems/TurnOutcome";
export { formatTime } from "./ChatTimelineItems/format";
export { ChatPromptSender } from "./ChatTimelineItems/McpStatusRow";
export { ChatHostId, CompactionMarker, ContinuedMarker, ProviderSwitchMarker } from "./ChatTimelineItems/markers";
export { User } from "lucide-react";
