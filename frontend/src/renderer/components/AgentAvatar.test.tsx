import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AgentAvatar } from "./AgentAvatar";
import claudeCodeLogo from "../assets/agents/claude-code.svg";
import codexLogo from "../assets/agents/codex.svg";
import kiroLogo from "../assets/agents/kiro.svg";

describe("AgentAvatar", () => {
	it("renders the bundled brand mark of every supported agent", () => {
		render(
			<>
				<AgentAvatar provider="claude-code" />
				<AgentAvatar provider="codex" />
				<AgentAvatar provider="kiro" />
			</>,
		);

		expect(screen.getByRole("img", { name: "claude-code" })).toHaveAttribute("src", claudeCodeLogo);
		expect(screen.getByRole("img", { name: "codex" })).toHaveAttribute("src", codexLogo);
		expect(screen.getByRole("img", { name: "kiro" })).toHaveAttribute("src", kiroLogo);
	});

	it("falls back to a lettered tile for a session saved under a removed agent", () => {
		render(<AgentAvatar provider="opencode" />);

		const tile = screen.getByRole("img", { name: "opencode" });
		expect(tile.tagName).not.toBe("IMG");
		expect(tile).toHaveTextContent("O");
	});
});
