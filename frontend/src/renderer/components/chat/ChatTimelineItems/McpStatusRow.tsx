import { createContext, useContext, useState } from "react";
import { useTranslation } from "react-i18next";
import { Plug } from "lucide-react";
import { type ConversationActivity } from "../../../types/conversation";

/**
 * Sends a line to the agent as the next message. The MCP notice uses it to run
 * the agent's own `/mcp` command; absent where the chat cannot send.
 */
export const ChatPromptSender = createContext<((text: string) => Promise<unknown> | void) | undefined>(undefined);

const ACTION_CLASS =
	"rounded text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:cursor-default disabled:opacity-60 disabled:no-underline";

/**
 * A failed MCP server, with which server, the error, and what to do. "Reconnect"
 * and "Show status" ask the agent's `/mcp` command, which answers in the chat.
 */
export function McpStatusRow({ activity }: { activity: ConversationActivity }) {
	const { t } = useTranslation();
	const send = useContext(ChatPromptSender);
	const [sent, setSent] = useState<"reconnect" | "status" | null>(null);
	const server = typeof activity.detail?.server === "string" ? activity.detail.server : "";
	const reason = typeof activity.detail?.text === "string" ? activity.detail.text : "";
	const status = activity.detail?.status;
	const ask = (action: "reconnect" | "status") => {
		if (!send) return;
		setSent(action);
		const text = action === "reconnect" ? `/mcp reconnect ${server}` : "/mcp";
		// Disabled only while the send is in flight: the daemon reports a server's
		// failure once, so a failed reconnect has no new notice to retry from.
		void Promise.resolve(send(text)).catch(() => {}).finally(() => setSent(null));
	};
	return (
		<div className="flex items-start gap-2.5 rounded-md border border-destructive/40 bg-surface px-3 py-2" data-testid="mcp-status-row">
			<Plug aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-destructive" />
			<div className="flex min-w-0 flex-col gap-1">
				<strong className="text-xs font-medium text-destructive">{activity.summary}</strong>
				{reason ? <span className="break-words text-xs leading-snug text-muted-foreground">{reason}</span> : null}
				<span className="text-xs leading-snug text-muted-foreground">
					{status === "needs-auth" ? t("chat.mcp.hintNeedsAuth") : t("chat.mcp.hint")}
				</span>
				{send && server ? (
					<div className="flex items-center gap-3">
						{status !== "needs-auth" ? (
							<button type="button" className={ACTION_CLASS} disabled={sent !== null} onClick={() => ask("reconnect")}>
								{t("chat.mcp.reconnect")}
							</button>
						) : null}
						<button type="button" className={ACTION_CLASS} disabled={sent !== null} onClick={() => ask("status")}>
							{t("chat.mcp.showStatus")}
						</button>
					</div>
				) : null}
			</div>
		</div>
	);
}
