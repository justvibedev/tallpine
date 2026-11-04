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
