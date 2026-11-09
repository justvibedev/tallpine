# Shared by scripts/local-ci/loop.sh and scripts/release-local.d/lock.sh: how a
# script talks to the tallpine CLI as the owner, not as whichever session it
# was started from. Source it; it defines functions only.

# tp_owner_cli <cli> <args...>: runs the CLI with TALLPINE_SESSION_* dropped so
# a script started inside a worker session cannot send as that session. A
# caller that needs a specific daemon exports TALLPINE_DATA_DIR / TALLPINE_RUN_FILE.
tp_owner_cli() {
  env -u TALLPINE_SESSION_ID -u TALLPINE_SESSION_TOKEN -u TALLPINE_SESSION_TOKEN_RUN_FILE -u TALLPINE_SESSION_AUTH -u TALLPINE_SESSION_AUTH_RUN_FILE \
    -u TALLPINE_PROJECT_ID -u VO_SESSION_ID "$@"
}

# tp_retry_until <deadline-epoch> <interval-seconds> <cmd...>: runs cmd until it
# succeeds or the deadline passes (one attempt always happens). Returns cmd's last status.
tp_retry_until() {
  local deadline="$1" interval="$2" rc
  shift 2
  while :; do
    "$@" && return 0
    rc=$?
    [ "$(date +%s)" -ge "$deadline" ] && return "$rc"
    sleep "$interval"
  done
}

# tp_notify_orchestrator <cli> <project> <target> <message>: sends ONE line to the project's
# orchestrator and prints one status line (callers add their own prefix); never fails. `orchestrator
# notify` picks the live target (<target> if live, else the oldest live orchestrator) and acts as the
# machine. Shared by loop.sh (send_to_orchestrator) and release-local's detached build (notify_build).
tp_notify_orchestrator() {
  local cli="$1" project="$2" target="$3" msg="$4" err sid
  if "${cli}" orchestrator --help 2>&1 | grep -qw notify; then # the parent prints its help (rc 0) even without the subcommand
    err="$("${cli}" orchestrator notify --project "${project}" --target "${target}" --message "${msg}" 2>&1 >/dev/null)" \
      && echo "notified the orchestrator" || echo "notify failed: ${err:-no output}"
    return 0
  fi
  # One-release fallback: an installed app older than `orchestrator notify`. Remove once it is installed.
  sid="$("${cli}" orchestrator ls --json 2>/dev/null | python3 -I -c 'import json,sys
p,t=sys.argv[1],sys.argv[2]
d=json.load(sys.stdin); xs=d if isinstance(d,list) else next((v for v in d.values() if isinstance(v,list)),[])
live=[x for x in xs if x.get("projectId")==p and not x.get("isTerminated")]
if not live: sys.exit()
print(t if any(x["id"]==t for x in live) else min(live,key=lambda x:x.get("createdAt",""))["id"])
' "${project}" "${target}" 2>/dev/null || true)"
  [[ -n "${sid}" ]] || { echo "notify: no live orchestrator, skipping"; return 0; }
  err="$("${cli}" send --session "${sid}" --message "${msg}" 2>&1 >/dev/null)" && echo "notified ${sid}" || echo "notify ${sid} failed: ${err:-no output}"
}
