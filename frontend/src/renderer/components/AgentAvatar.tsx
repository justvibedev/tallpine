import {
	AgentAvatar as ProductAgentAvatar,
	type AgentAvatarProps,
	type AgentLogoSources,
} from "@tallpine/product-ui";
import claudeLogo from "../assets/agents/claude.svg";
import claudeCodeLogo from "../assets/agents/claude-code.svg";
import codexLogo from "../assets/agents/codex.svg";
import deepseekHarnessDarkLogo from "../assets/agents/deepseek-harness-dark.svg";
import deepseekHarnessLightLogo from "../assets/agents/deepseek-harness-light.svg";
import kiroLogo from "../assets/agents/kiro.svg";

// Official brand marks keyed by the harness name Tallpine stores on session.provider.
// A { light, dark } pair is used where the brand ships separate marks for each
// background; a single mark reads on both. Agents without an asset fall back to
// a lettered tile.
const LOGOS: AgentLogoSources = {
	codex: codexLogo,
	"claude-code": claudeCodeLogo,
	claude: claudeLogo,
	kiro: kiroLogo,
	"deepseek-harness": { light: deepseekHarnessLightLogo, dark: deepseekHarnessDarkLogo },
};

/**
 * Agent mark for board/task cards: the harness's real brand logo rendered bare —
 * no tile, border, or background — so each brand's own shape shows (codex,
 * claude, kiro carry their own rounded backgrounds). Agents without an
 * asset fall back to a bare initial. Kept small so the title stays the hero.
 *
 * The provider is exposed as the accessible name (alt / aria-label), not just a
 * hover title, so surfaces that show the logo in place of visible agent text —
 * e.g. the archive cards — still name the agent for screen readers.
 */
export function AgentAvatar({ provider, className, decorative = false }: AgentAvatarProps) {
	return <ProductAgentAvatar className={className} decorative={decorative} logoSources={LOGOS} provider={provider} />;
}

export type { AgentAvatarProps };
