import { useQuery } from "@tanstack/react-query";
import { listProjectAsks, projectAsksKey } from "../../lib/ask-api";
import { asksForChat, laneAsksElsewhere } from "./askModel";

/** The tracked asks this chat shows; one shared query for the strip and the empty-state gate. */
export function useChatAsks(projectId: string | undefined, lane: string | undefined) {
	const query = useQuery({
		queryKey: projectAsksKey(projectId ?? ""),
		queryFn: () => listProjectAsks(projectId ?? ""),
		enabled: Boolean(projectId),
		retry: false,
	});
	const all = query.data ?? [];
	return { all, asks: asksForChat(all, lane), elsewhere: laneAsksElsewhere(all, lane) };
}
