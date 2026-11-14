/** An owner ask as the daemon lists it (GET /api/v1/projects/{projectId}/asks). */
export type AskOption = { id: string; label: string };
export type OwnerAsk = {
	id: string;
	ref: string;
	lane?: string;
	text: string;
	reason?: string;
	options: AskOption[];
	recommended: string;
	state: "open" | "answered" | "withdrawn";
	answer?: { text: string };
	reaskOf?: string;
	reaskReason?: string;
};

/** The open asks that belong in one chat: a lane's chat holds its lane's, the main chat the un-laned ones. */
export function asksForChat(asks: readonly OwnerAsk[], lane: string | undefined): OwnerAsk[] {
	return asks.filter((a) => a.state === "open" && (lane ? a.lane === lane : !a.lane));
}

/** Open lane asks, which the main chat only points to. */
export function laneAsksElsewhere(asks: readonly OwnerAsk[], lane: string | undefined): OwnerAsk[] {
	return lane ? [] : asks.filter((a) => a.state === "open" && a.lane);
}

/** The answered ask a re-asked one repeats, for the "asked again" line. */
export function earlierAsk(asks: readonly OwnerAsk[], ask: OwnerAsk): OwnerAsk | undefined {
	return ask.reaskOf ? asks.find((a) => a.ref === ask.reaskOf) : undefined;
}
