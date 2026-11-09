import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchGitHubAccountCheck } from "../lib/github-account-check";
import { ProjectGitHubAccountCheck } from "./ProjectGitHubAccountCheck";

vi.mock("../lib/github-account-check", async (orig) => ({ ...(await orig<object>()), fetchGitHubAccountCheck: vi.fn() }));

function renderCheck() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><ProjectGitHubAccountCheck projectId="p" /></QueryClientProvider>);
}

describe("ProjectGitHubAccountCheck", () => {
	beforeEach(() => vi.mocked(fetchGitHubAccountCheck).mockReset());

	it("warns when the token is another account than the override", async () => {
		vi.mocked(fetchGitHubAccountCheck).mockResolvedValue({ status: "mismatch", override: "acme", login: "acme", tokenLogin: "justvibedev", repo: "acme/repo" });
		renderCheck();
		const alert = await screen.findByRole("alert");
		expect(alert.textContent).toContain("acme");
		expect(alert.textContent).toContain("justvibedev");
	});

	it("shows the matching login without a warning", async () => {
		vi.mocked(fetchGitHubAccountCheck).mockResolvedValue({ status: "ok", override: "a", login: "a", tokenLogin: "a", repo: "a/r" });
		renderCheck();
		const status = await screen.findByText(/Sessions use a, which can push to a\/r/);
		expect(status.closest("[data-testid]")?.getAttribute("role")).toBe("status");
	});

	it("warns when the account cannot see the repo", async () => {
		vi.mocked(fetchGitHubAccountCheck).mockResolvedValue({ status: "no_repo_access", override: "a", login: "a", tokenLogin: "a", repo: "o/r" });
		renderCheck();
		expect((await screen.findByRole("alert")).textContent).toContain("cannot push to o/r");
	});

	it("explains a non-GitHub origin where Auto-pick cannot choose", async () => {
		vi.mocked(fetchGitHubAccountCheck).mockResolvedValue({ status: "no_account", override: "", login: "", tokenLogin: "", repo: "" });
		renderCheck();
		expect((await screen.findByText(/origin is not on GitHub, so Auto-pick cannot choose.*machine's default GitHub login/)).textContent).toContain("unless you pick an account");
	});

	it("says a pinned account is used for GitHub even when the origin is elsewhere", async () => {
		vi.mocked(fetchGitHubAccountCheck).mockResolvedValue({ status: "no_account", override: "acme", login: "", tokenLogin: "", repo: "" });
		renderCheck();
		expect(await screen.findByText(/origin is not on GitHub\. Sessions use acme for GitHub commands/)).toBeInTheDocument();
	});
});
