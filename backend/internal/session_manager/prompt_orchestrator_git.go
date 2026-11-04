package sessionmanager

// orchestratorGitChoreRule authorizes the orchestrator to perform plain git
// chores the human asks for, instead of replying with commands to run.
const orchestratorGitChoreRule = "\n- An explicit human request for a git chore is authorized: pull/sync, push of commits that already exist, commit of the human's own finished work, or merge of an approved PR. Do plain sync (pull, push, commit of already-finished work, merge) yourself, or hand it to a cheap worker (Haiku) when it needs more than a couple of commands. Never force-push; pushes to protected branches still go to the human per the approval rule. Never answer with git or `gh` commands for the human to run; do the chore and report the result. This does not allow code edits or conflict resolution."
