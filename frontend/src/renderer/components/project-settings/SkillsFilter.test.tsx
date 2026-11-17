import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ProjectTextArea } from "./ProjectTextArea";
import { sharedNote } from "./SkillsFilter";

describe("sharedNote", () => {
	it("is the note most rows share", () => {
		expect(sharedNote(["a", "a", "a", "b"])).toBe("a");
	});
	it("is none when notes differ, so no row's note is promoted above the list", () => {
		expect(sharedNote(["a", "b"])).toBeUndefined();
		expect(sharedNote(["a", undefined, undefined])).toBeUndefined();
		expect(sharedNote(["a"])).toBeUndefined();
	});
});

describe("ProjectTextArea", () => {
	it("only the rules boxes (autoGrow) get the tall auto-growing sizing", () => {
		render(<><ProjectTextArea id="a" label="grow" rows={5} autoGrow value="" onChange={() => {}} /><ProjectTextArea id="b" label="plain" rows={3} value="" onChange={() => {}} /></>);
		expect(screen.getByLabelText("grow").className).toContain("min-h-40");
		expect(screen.getByLabelText("plain").className).not.toContain("min-h-40");
		expect(screen.getByLabelText("plain").className).not.toContain("field-sizing-content");
	});
});
