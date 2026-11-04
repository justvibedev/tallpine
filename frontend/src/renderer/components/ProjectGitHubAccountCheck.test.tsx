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
		const status = await screen.findByText(/Token is a and can push to a\/r/);
		expect(status.closest("[data-testid]")?.getAttribute("role")).toBe("status");
	});

	it("warns when the account cannot see the repo", async () => {
		vi.mocked(fetchGitHubAccountCheck).mockResolvedValue({ status: "no_repo_access", override: "a", login: "a", tokenLogin: "a", repo: "o/r" });
		renderCheck();
		expect((await screen.findByRole("alert")).textContent).toContain("cannot see or push to o/r");
	});
});
