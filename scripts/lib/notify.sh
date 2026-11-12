# Shared by scripts/local-ci/loop.sh and release-local's detached build: how a script run by launchd
# tells the main-health orchestrator something. Source it; it defines functions only.
#
# B631: a signed daemon refuses API calls from processes Tallpine did not start, and these scripts are
# such processes, so they no longer message an orchestrator. They append the alert to alerts.tsv next
# to local-ci.tsv instead. The daemon reads it every minute and passes new alerts to the session in
# <local-ci>/notify-target (B680, backend/internal/service/localcialerts); scripts/local-ci/alerts is
# the manual reader sharing its cursor. While no reader runs (the app is closed, or no notify-target),
# each alert is also shown as a macOS notification, so none is parked unseen.

# tp_record_alert <alerts-file> <message>: appends "<UTC time><TAB><message>" as one line (tabs and
# newlines in the message become spaces) and prints one status line; never fails. When no reader has
# passed alerts on lately (tp_alert_reader_stale), it also says so and shows the alert on the desktop,
# so an alert is never silently parked while the automation is missing or broken.
tp_record_alert() {
  local file="$1" msg
  msg="$(printf '%s' "$2" | tr '\t\n' '  ')"
  if mkdir -p "$(dirname "${file}")" 2>/dev/null && printf '%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${msg}" >>"${file}" 2>/dev/null; then
    echo "alert recorded in ${file}"
  else
    echo "could not record the alert in ${file}"
  fi
  if tp_alert_reader_stale "$(dirname "${file}")/alerts.cursor"; then
    echo "WARNING: no reader has passed local-CI alerts on in the last ${TALLPINE_ALERT_READER_STALE:-3600}s; the Tallpine app passes them on while it runs, to the orchestrator named in the local-CI notify-target file (scripts/local-ci/alerts --help). Showing this alert on the desktop instead."
    tp_desktop_notify "${msg}"
  fi
  return 0
}

# tp_alert_defs: prints what a detached `bash -c` child (started under env -i) needs to call
# tp_record_alert: every helper it uses and the TALLPINE_ALERT_* settings that are set. Callers append
# it to their own declare -f output instead of listing these helpers themselves.
tp_alert_defs() {
  declare -f tp_alert_defs tp_record_alert tp_alert_reader_stale tp_desktop_notify
  declare -p TALLPINE_ALERT_DESKTOP_NOTIFY TALLPINE_ALERT_READER_STALE 2>/dev/null
  return 0
}

# tp_alert_reader_stale <cursor-file>: true when scripts/local-ci/alerts, which stamps the cursor on
# every run, has never run or not within TALLPINE_ALERT_READER_STALE seconds (default 3600, two runs
# of the suggested */30 schedule).
tp_alert_reader_stale() {
  local mtime
  mtime="$(stat -f %m "$1" 2>/dev/null || stat -c %Y "$1" 2>/dev/null)" || return 0
  [ -n "${mtime}" ] || return 0
  [ $(($(date +%s) - mtime)) -gt "${TALLPINE_ALERT_READER_STALE:-3600}" ]
}

# tp_desktop_notify <message>: shows a macOS notification as the owner, outside the daemon (which
# refuses these launchd scripts); never fails. TALLPINE_ALERT_DESKTOP_NOTIFY replaces the command
# (tests point it at a recorder; empty turns it off).
tp_desktop_notify() {
  local cmd="${TALLPINE_ALERT_DESKTOP_NOTIFY-osascript}"
  [ -n "${cmd}" ] && command -v "${cmd}" >/dev/null 2>&1 || return 0
  "${cmd}" -e 'on run argv' -e 'display notification (item 1 of argv) with title "Tallpine local CI"' -e 'end run' "$1" >/dev/null 2>&1 || true
}
