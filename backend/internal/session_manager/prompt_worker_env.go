package sessionmanager

// workerContainerLabelPrompt tells a worker how to make any Docker containers
// it starts reapable by Tallpine on session end (#2652), and where scratch files
// and background processes belong. Tallpine does not run docker
// itself -- this is the only place the tallpine.session/tallpine.spare convention reaches
// an agent.
func workerContainerLabelPrompt() string {
	return `## Docker Containers Started By This Session

If this task starts its own Docker containers (a local database, a queue, any ad-hoc service), label every one so Tallpine can find and remove it when this session ends:

- Add ` + "`" + `--label tallpine.session=$TALLPINE_SESSION_ID` + "`" + ` to every ` + "`" + `docker run` + "`" + `. Tallpine force-removes containers carrying this label when the session is killed or otherwise terminates.
- If a container is deliberately shared substrate that must outlive this session (a shared postgres, a registry), also add ` + "`" + `--label tallpine.spare=true` + "`" + ` -- Tallpine never reaps a spared container.
- Without the ` + "`" + `tallpine.session` + "`" + ` label, a container you start is not tracked and will not be cleaned up automatically.

## Scratch Files and Background Processes

- Put scratch files (test HOMEs, clones, build output) under ` + "`" + `$TMPDIR` + "`" + `, never bare ` + "`" + `/tmp` + "`" + `: ` + "`" + `$TMPDIR` + "`" + ` is this session's own directory and is deleted when the session ends. When you scrub the environment (` + "`" + `env -i` + "`" + `), pass ` + "`" + `TMPDIR=$TMPDIR` + "`" + ` through.
- Stop every dev server, headless browser and simulator you started before you report done. Tallpine stops processes left running by an ended session, but only the ones it can trace to this session: any process that inherited TALLPINE_SESSION_ID, including one detached with setsid, nohup or &, and its descendants, is killed when the session is killed. Never start a shared or long-lived service (such as the CI loop) from a session shell; have launchd own it.`
}

// testParallelismPrompt tells an agent to cap test parallelism: several agents
// share one machine. Launch sets TALLPINE_TEST_PARALLELISM and GOMAXPROCS
// (agentlaunch.ApplyParallelismDefaults); a project or daemon env may override.
func testParallelismPrompt() string {
	return "## Test Parallelism\n\n" +
		"Several agents share this machine. `$TALLPINE_TEST_PARALLELISM` holds your parallelism budget N and `GOMAXPROCS` is already set to it. Pass N to every test runner instead of its default (all cores): `go test -p $TALLPINE_TEST_PARALLELISM`, `vitest --maxWorkers=$TALLPINE_TEST_PARALLELISM` / `jest --maxWorkers=$TALLPINE_TEST_PARALLELISM`, `pytest -n $TALLPINE_TEST_PARALLELISM`. Never raise it, and do not start several suites at once.\n\n" +
		"Reuse a run already in flight; never duplicate it:\n\n" +
		"- Before starting a test, typecheck or lint run, check whether you already have that command running: your own background tasks, or a command a timeout moved to the background. Only a run you started in this worktree counts; never wait on or kill a process from another session (other agents run the same commands in their own worktrees, so do not use machine-wide `pgrep` matches as proof). If your run is still going, wait for it instead of starting another. A tool timeout does not stop the command; it keeps running in the background.\n" +
		"- Keep one waiter per run. Do not add a second poller, monitor or `sleep`/`pgrep` loop because a turn ended, a notification arrived for something else, or a wait timed out; re-arm at most one waiter on the same run, and only after the old one has expired.\n" +
		"- Redirect the run's output to a file under `$TMPDIR` when you start it, and read results from that file. Do not rely on a task-output path you have to glob for.\n" +
		"- Stop a superseded run of yours (`kill` it) before you start its replacement, for example after you edit the code a running suite was testing. Never leave the old run going beside the new one.\n" +
		"- Waiting on one of your own background runs is not idling. If your harness notifies you when a background task finishes, end the turn or do other work and let that notification wake you; otherwise wait on the run in the foreground with your single waiter."
}
