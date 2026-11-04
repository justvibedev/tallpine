#!/usr/bin/env bash
set -euo pipefail

fixture_parent="${TALLPINE_ONBOARDING_FIXTURE_PARENT:-$HOME/dev/tallpine-onboarding-demo/fixtures}"
mkdir -p "$fixture_parent"
fixture_root="$(mktemp -d "$fixture_parent/run.XXXXXX")"
fixture_git="$(command -v git)"
mkdir -p "$fixture_root/source" "$fixture_root/available" "$fixture_root/conflict/sample" "$fixture_root/reuse" "$fixture_root/bin"
git init -q -b main "$fixture_root/source"
echo 'Onboarding test repository' > "$fixture_root/source/README.md"
git -C "$fixture_root/source" add README.md
git -C "$fixture_root/source" -c user.name='Tallpine test' -c user.email='tallpine-test@example.invalid' commit -qm 'Initial test commit'
git clone -q --bare "$fixture_root/source" "$fixture_root/sample.git"
git clone -q "file://$fixture_root/sample.git" "$fixture_root/reuse/sample"
echo 'Leave this file alone' > "$fixture_root/conflict/sample/keep.txt"

# A test-only Git wrapper. Normal commands go to the real binary; only clone
# receives the selected fault. Each fixture directory owns its retry counter.
# Copied, not a heredoc: the ~600-byte body would deadlock bash on macOS when pipes
# shrink to 512 bytes under memory pressure (B23).
cp "$(dirname "${BASH_SOURCE[0]}")/git-wrapper.sh" "$fixture_root/bin/git"
chmod +x "$fixture_root/bin/git"

printf 'Fixture directory: %s\nRepository URL: file://%s/sample.git\nAvailable destination: %s/available\nConflicting destination: %s/conflict\nExisting checkout destination: %s/reuse\nReal Git binary: %s\n' \
  "$fixture_root" "$fixture_root" "$fixture_root" "$fixture_root" "$fixture_root" "$fixture_git"
