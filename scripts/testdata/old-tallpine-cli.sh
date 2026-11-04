#!/bin/sh
echo "$@" >>"$CALLS"
case "$1 $2" in
  "orchestrator --help") printf 'Manage orchestrator sessions\n\nAvailable Commands:\n  ls  List\n  new  New\n'; exit 0 ;;
  "orchestrator notify") case "$*" in *--help*) printf "Available Commands:\n  ls  List\n"; exit 0 ;; esac; echo "Error: unknown flag: --project" >&2; exit 1 ;;
  "orchestrator ls") echo '{"data":[{"id":"orch-old","projectId":"agent-orchestrator","isTerminated":false,"createdAt":"2026-01-01T00:00:00Z"}]}'; exit 0 ;;
esac
exit 0
