# Switch Claude subscription

Move every session to a different Claude subscription without losing a conversation.

## Switch

1. Open **Settings → Accounts → Claude Code**.
2. Select **+** (Add account) and sign in with the subscription you want. The sign-in runs in its own folder; your computer's login is not touched.
3. On the new account's row, select **Use for all sessions**.
4. Check the list of sessions that will move. Orchestrators move last.
5. Choose what happens to a session that is working:
   - **Wait for its turn to finish** (default): the session switches when its current turn ends. It shows as _Busy, waiting for its turn_ until then.
   - **Stop its turn now**: the turn is cancelled and the session switches at once.
6. Select **Switch sessions**. The account becomes the default for new sessions, and a progress list shows each session as it switches. You can close the window; the switch continues.

A session whose agent is not running only changes account. It is not started.

If a session fails (for example its conversation is not visible to the new account), it stays on its old account and the reason is shown. The other sessions still switch. After fixing the cause, run **Use for all sessions** again: only sessions not yet on the account are moved.

## Do not log out the computer's login to switch

**Sign in again** and **Logout** on the **This computer** row change the login that every session without an account folder uses, while those sessions are running. Use **Add account** and **Use for all sessions** instead. They keep the old login signed in and move the sessions one by one.

## What to expect

- **The first turn after a switch is slower and uses more of the new subscription.** The prompt cache does not carry over between accounts, so that turn rebuilds the whole context. Later turns are normal.
- **Sonnet and Haiku thinking from the old account is dropped.** Claude binds those thinking blocks to the account that produced them. Opus and Fable thinking carries over. The conversation text, tool results and files are unaffected.
- **History stays in one place.** Account folders link to your computer's `~/.claude` for projects, tasks, plans, skills and more, so the new account sees the same conversations.

## When Use for all sessions is refused

- **Not signed in**: sign the account in again from its row.
- **The account keeps its own history** (`AGENT_ACCOUNT_SPLIT_HISTORY`): its folder holds real `projects` or `tasks` instead of links to your computer's. Conversations from other accounts could not resume there, so the switch is refused before anything changes. Follow the message: move its contents into `~/.claude/projects`, or remove the account and add it again.
- **Another switch is running** (`ADOPTION_IN_PROGRESS`): wait for it to finish, then retry. A session that waits for a busy turn keeps its switch running, with no deadline. To get out, let that turn finish, or open the session's inspector and cancel its move; the switch then ends for that session and a new one can start. There is no single cancel for the whole switch yet.

## Signing in again to a saved account

If **Sign in again** ends with a different login than the account's own, or with the computer's own login, Tallpine does not run Claude Code's sign-out (it would revoke that login's tokens on Anthropic's side). It deletes the credentials file in that account's folder, marks the account signed out and says so. Sign in again with the right login to use it. A login stored in the macOS Keychain for that folder is not removed.

## Removing an account

**Remove** stops the agents on that account and deletes its folder in `~/.tallpine`. It does not sign out through Claude Code, because that also revokes tokens on Anthropic's side and cannot be shown to leave other logins of the same user alone. The saved sign-in stays in the macOS Keychain under `Claude Code-credentials-<hash>` and is not revoked. To revoke it, sign out of that account on claude.ai.
