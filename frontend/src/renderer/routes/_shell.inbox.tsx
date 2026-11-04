import { createFileRoute } from "@tanstack/react-router";
import { InboxPage } from "../components/inbox/InboxPage";

type InboxSearch = { item?: string };

export const Route = createFileRoute("/_shell/inbox")({
	validateSearch: (search: Record<string, unknown>): InboxSearch => ({ item: typeof search.item === "string" && search.item !== "" ? search.item : undefined }),
	component: InboxPage,
});
