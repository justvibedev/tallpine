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
