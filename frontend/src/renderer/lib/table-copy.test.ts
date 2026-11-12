import { describe, expect, it } from "vitest";
import { toCsv, toMarkdown } from "./table-copy";

describe("table-copy", () => {
	it("quotes CSV fields", () => {
		expect(toCsv([["a", "b,c"], ['say "hi"', "x\ny"]])).toBe('a,"b,c"\r\n"say ""hi""","x\ny"');
	});
	it("escapes pipes in markdown", () => {
		expect(toMarkdown([["h1", "h2"], ["a|b", "c"]])).toBe("| h1 | h2 |\n| --- | --- |\n| a\\|b | c |");
	});
});
