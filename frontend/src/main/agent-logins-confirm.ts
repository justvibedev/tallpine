import type { AgentLoginsConfirm } from "./browser-view-host/agent-partition";

type MessageBox = (options: {
	type: "warning";
	title: string;
	message: string;
	detail: string;
	buttons: string[];
	defaultId: number;
	cancelId: number;
}) => Promise<{ response: number }>;

// One-time native confirm shown when an agent first needs a browser tab in a
// worker whose browser profile holds the person's logins. Declining (or closing
// the dialog) keeps the agent in a throwaway profile with no logins.
export function createAgentLoginsConfirm(showMessageBox: MessageBox): AgentLoginsConfirm {
	return async (sessionId, profileName) => {
		const result = await showMessageBox({
			type: "warning",
			title: "Let the agent use your logins?",
			message: profileName
				? `Worker ${sessionId} wants to browse with the “${profileName}” profile.`
				: `Worker ${sessionId} wants to browse with a browser profile.`,
			detail:
				"This profile may be signed in to your accounts. Agent tabs otherwise start in a temporary profile with no logins. This choice applies to this worker until the app restarts.",
			buttons: ["Don’t share logins", "Use my logins"],
			defaultId: 0,
			cancelId: 0,
		});
		return result.response === 1;
	};
}
