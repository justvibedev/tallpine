import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { backlogApi, renderBacklog, resetBacklogApi } from "../../test/backlog-harness";
import { BacklogCapturePopover, splitCapture } from "./BacklogCapturePopover";

vi.mock("../../lib/backlog-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/backlog-api")>()), ...(await import("../../test/backlog-harness")).backlogApi }));

const projects = [{ id: "proj-1", name: "Project One" }, { id: "proj-2", name: "Project Two" }];

function popover(overrides: Partial<React.ComponentProps<typeof BacklogCapturePopover>> = {}) {
	const props = { open: true, initialText: "", projects, defaultProjectId: "proj-1" as string | undefined, onOpenChange: vi.fn(), onAdded: vi.fn(), ...overrides };
	renderBacklog(<BacklogCapturePopover {...props} />);
	return props;
}
const field = () => screen.getByRole("textbox", { name: "Title and note" });

beforeEach(() => resetBacklogApi([]));

describe("splitCapture", () => {
	it("takes the first line as the title and the rest as the note", () => {
		expect(splitCapture("Fix it")).toEqual({ title: "Fix it", note: "" });
		expect(splitCapture("  \n Fix it \n\n  more detail\nand more ")).toEqual({ title: "Fix it", note: "more detail\nand more" });
	});
});

describe("BacklogCapturePopover", () => {
	it("adds the item on Enter into the chosen project, already open, and reports it", async () => {
		const props = popover();
		await userEvent.type(field(), "Restart shows a duplicate notice{Enter}");
		await waitFor(() => expect(backlogApi.createBacklogItem).toHaveBeenCalledWith("proj-1", { title: "Restart shows a duplicate notice", note: "" }));
		await waitFor(() => expect(props.onAdded).toHaveBeenCalledWith(expect.objectContaining({ ref: "B99", title: "Restart shows a duplicate notice" })));
		expect(props.onOpenChange).toHaveBeenCalledWith(false);
	});

	it("Shift+Enter starts a note instead of adding", async () => {
		popover();
		await userEvent.type(field(), "Title{Shift>}{Enter}{/Shift}body line");
		expect(backlogApi.createBacklogItem).not.toHaveBeenCalled();
		expect(field()).toHaveValue("Title\nbody line");
		await userEvent.type(field(), "{Enter}");
		await waitFor(() => expect(backlogApi.createBacklogItem).toHaveBeenCalledWith("proj-1", { title: "Title", note: "body line" }));
	});

	it("does not add an empty title", async () => {
		popover();
		expect(screen.getByRole("button", { name: "Add" })).toBeDisabled();
		await userEvent.type(field(), "   {Enter}");
		expect(backlogApi.createBacklogItem).not.toHaveBeenCalled();
	});

	it("refuses a title over 200 characters with a message, without calling the API", async () => {
		popover();
		await userEvent.click(field());
		await userEvent.paste("x".repeat(201));
		await userEvent.keyboard("{Enter}");
		expect(await screen.findByRole("alert")).toHaveTextContent("at most 200 characters");
		expect(backlogApi.createBacklogItem).not.toHaveBeenCalled();
	});

	it("files into the project picked in the popover", async () => {
		popover();
		await userEvent.click(screen.getByRole("combobox", { name: "Project" }));
		await userEvent.click(await screen.findByRole("option", { name: "Project Two" }));
		await userEvent.type(field(), "Elsewhere{Enter}");
		await waitFor(() => expect(backlogApi.createBacklogItem).toHaveBeenCalledWith("proj-2", expect.anything()));
	});

	it("starts from the text the caller typed and keeps the popover open with the message when the server refuses", async () => {
		backlogApi.createBacklogItem.mockRejectedValue(new Error("inbox full"));
		const props = popover({ initialText: "From the palette" });
		expect(field()).toHaveValue("From the palette");
		await userEvent.type(field(), "{Enter}");
		expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't add to the backlog: inbox full");
		expect(props.onOpenChange).not.toHaveBeenCalled();
		expect(props.onAdded).not.toHaveBeenCalled();
	});

	it("falls back to the first project when none is in view", async () => {
		popover({ defaultProjectId: undefined });
		await userEvent.type(field(), "Anywhere{Enter}");
		await waitFor(() => expect(backlogApi.createBacklogItem).toHaveBeenCalledWith("proj-1", expect.anything()));
	});
});
