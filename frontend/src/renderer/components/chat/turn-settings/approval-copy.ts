/** Approval mode copy and ordering per harness. */
import type { ApprovalMode } from "../../../types/conversation";

/** Tallpine's generic approval modes, used by harnesses without a native vocabulary. */
export const APPROVAL_COPY: Record<ApprovalMode, { label: string; description: string }> = {
	default: { label: "Use agent permissions", description: "Follows the agent's own permission settings" },
	"accept-edits": { label: "Accept edits", description: "File edits apply without asking" },
	auto: { label: "Auto-approve", description: "Approves routine actions and asks about risky ones" },
	"bypass-permissions": { label: "Bypass permissions", description: "Never asks before acting" },
};

export const APPROVAL_ORDER: ApprovalMode[] = [
	"default",
	"accept-edits",
	"auto",
	"bypass-permissions",
];

// Codex has three distinct permission profiles. Its default is already full
// access in Tallpine's isolated worktree posture, so expose it as that rather than a
// fourth, ambiguous "default" option.
export const CODEX_APPROVAL_COPY: Record<ApprovalMode, { label: string; description: string }> = {
	default: { label: "Full access", description: "Edits files and runs commands without asking" },
	"accept-edits": { label: "Ask for approval", description: "Asks before edits and commands that need it" },
	auto: { label: "Approve for me", description: "Only asks about actions detected as potentially unsafe" },
	"bypass-permissions": { label: "Bypass permissions", description: "Never asks before acting" },
};

export const CODEX_APPROVAL_ORDER: ApprovalMode[] = [
	"default",
	"accept-edits",
	"auto",
	"bypass-permissions",
];

export const TRIGGER_CLASS =
	"h-7 gap-1 bg-transparent rounded-lg px-3 text-xs! leading-none text-muted-foreground hover:bg-interactive-active hover:text-foreground data-[state=open]:bg-interactive-active data-[state=open]:text-foreground";
export const CHAT_MENU_CLASS = "chat-settings-menu text-xs!";
