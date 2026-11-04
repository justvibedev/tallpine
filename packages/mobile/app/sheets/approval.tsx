import { useLocalSearchParams } from "expo-router";
import { ApprovalSheet } from "../../lib/ApprovalSheet";

// The approve/deny sheet for a worker's permission prompt, opened from an Inbox
// row or a needs-input push. See ApprovalSheet.
export default function ApprovalSheetRoute() {
	const { sessionId, hostId } = useLocalSearchParams<{ sessionId?: string; hostId?: string }>();
	if (!sessionId || !hostId) return null;
	return <ApprovalSheet sessionId={sessionId} hostId={hostId} />;
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
