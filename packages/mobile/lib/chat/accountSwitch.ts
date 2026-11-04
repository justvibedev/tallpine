/** The line under the "Switched account" notice: the next message continues on the new account. */
export function accountSwitchedDetail(accountId: unknown): string {
	return typeof accountId === "string" && accountId ? `Your next message continues on ${accountId}` : "Your next message continues on the new account";
}
