import { createFileRoute } from "@tanstack/react-router";
import { BacklogPage } from "../components/backlog/BacklogPage";

type BacklogSearch = { project?: string; item?: string };

const text = (value: unknown) => (typeof value === "string" && value !== "" ? value : undefined);

export const Route = createFileRoute("/_shell/backlog")({
	validateSearch: (search: Record<string, unknown>): BacklogSearch => ({ project: text(search.project), item: text(search.item) }),
	component: BacklogPage,
});
