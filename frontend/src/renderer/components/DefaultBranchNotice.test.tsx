import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { DefaultBranchNotice } from "./DefaultBranchNotice";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock, PUT: putMock }, apiErrorMessage: (e: { message?: string } | undefined) => e?.message ?? "request failed" }));

let check: Record<string, unknown>;
const onSwitch = vi.fn();
beforeEach(() => {
	check = { defaultBranch: "master", suggestion: { branch: "main", ahead: 22, newerDays: 1400, checkedOut: true } };
	getMock.mockReset().mockImplementation(async () => ({ data: check }));
	putMock.mockReset();
	onSwitch.mockReset();
});
const page = () => render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><DefaultBranchNotice projectId="p" onSwitch={onSwitch} /></QueryClientProvider>);

it("names both branches and offers the switch without changing anything", async () => {
	page();
	const notice = await screen.findByTestId("default-branch-notice");
	expect(notice).toHaveTextContent("New sessions start from master, but main has 22 more commits and is 1400 days newer.");
	expect(notice).toHaveTextContent("checked out in your project folder");
	expect(onSwitch).not.toHaveBeenCalled();
	expect(putMock).not.toHaveBeenCalled();
});

it("the switch hands the branch to the form, writes nothing itself and hides the notice", async () => {
	page();
	await userEvent.click(await screen.findByRole("button", { name: "Use main as the default branch" }));
	expect(onSwitch).toHaveBeenCalledExactlyOnceWith("main");
	expect(putMock).not.toHaveBeenCalled();
	await waitFor(() => expect(screen.queryByTestId("default-branch-notice")).not.toBeInTheDocument());
});

it("shows nothing when the default looks right", async () => {
	check = { defaultBranch: "main" };
	page();
	await waitFor(() => expect(getMock).toHaveBeenCalled());
	await new Promise((r) => setTimeout(r, 20));
	expect(screen.queryByTestId("default-branch-notice")).not.toBeInTheDocument();
});
