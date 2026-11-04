import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ProjectAutoReviewRow, ProjectRiskPathsRows, ProjectTerminateOnMergeRow } from "./ProjectReviewDefaultsRows";

describe("project review rows", () => {
	it("shows 'Use app default (On)' for an unset project and saves null, true and false", async () => {
		const onChange = vi.fn();
		render(<ProjectAutoReviewRow values={{ autoReview: null }} onChange={onChange} appDefault />);
		const menu = screen.getByLabelText("Auto review PRs");
		expect(menu).toHaveTextContent("Use app default (On)");
		await userEvent.click(menu);
		await userEvent.click(await screen.findByRole("menuitem", { name: "Off" }));
		expect(onChange).toHaveBeenLastCalledWith({ autoReview: false });
		await userEvent.click(menu);
		await userEvent.click(await screen.findByRole("menuitem", { name: "On" }));
		expect(onChange).toHaveBeenLastCalledWith({ autoReview: true });
	});

	it("saves null when a project goes back to the app default", async () => {
		const onChange = vi.fn();
		render(<ProjectAutoReviewRow values={{ autoReview: false }} onChange={onChange} appDefault={false} />);
		const menu = screen.getByLabelText("Auto review PRs");
		expect(menu).toHaveTextContent("Off");
		await userEvent.click(menu);
		await userEvent.click(await screen.findByRole("menuitem", { name: "Use app default (Off)" }));
		expect(onChange).toHaveBeenLastCalledWith({ autoReview: null });
	});

	it("shows a managed value read-only", () => {
		render(<ProjectAutoReviewRow values={{ autoReview: true }} onChange={vi.fn()} appDefault={false} locked />);
		expect(screen.getByTestId("project-auto-review-managed")).toHaveTextContent("Set by your organization: On");
		expect(screen.queryByLabelText("Auto review PRs")).toBeNull();
	});

	it("keeps end-on-merge a plain switch", () => {
		render(<ProjectTerminateOnMergeRow values={{ terminateOnPrMerge: true }} onChange={vi.fn()} />);
		expect(screen.getByRole("switch", { name: "End workers when their PRs merge" })).toBeChecked();
	});

	it("shows the built-in path list with a marker while the project sets none", () => {
		render(<ProjectRiskPathsRows values={{}} onChange={vi.fn()} />);
		const effective = screen.getByTestId("effective-risk-paths");
		expect(effective).toHaveTextContent("In effect: **/migrations/**");
		expect(effective).toHaveTextContent("built-in");
	});

	it("shows the project's own list without the built-in marker", () => {
		render(<ProjectRiskPathsRows values={{ reviewRiskPaths: "db/**, auth/**" }} onChange={vi.fn()} />);
		const effective = screen.getByTestId("effective-risk-paths");
		expect(effective).toHaveTextContent("In effect: db/**, auth/**");
		expect(effective).not.toHaveTextContent("built-in");
	});
});
