#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == clone ]]; then
  case "${TALLPINE_ONBOARDING_FAULT:-none}" in
    slow) sleep 75 ;;
    retry)
      count_file="$TALLPINE_ONBOARDING_FIXTURES/retry-attempts"
      attempt=0
      [[ ! -f "$count_file" ]] || read -r attempt < "$count_file"
      attempt=$((attempt + 1))
      echo "$attempt" > "$count_file"
      if (( attempt < 3 )); then
        echo 'fatal: connection reset' >&2
        exit 128
      fi
      ;;
    offline) echo 'fatal: Could not resolve host: example.invalid' >&2; exit 128 ;;
  esac
fi
exec "$TALLPINE_ONBOARDING_REAL_GIT" "$@"
