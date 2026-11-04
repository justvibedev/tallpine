# Self-hosted remote computers (experimental)

Sessions stay on the computer that started them. Desktop and mobile are clients.

## Install on Ubuntu

On a fresh Ubuntu 24.04 x64 machine, SSH in as a non-root user with `sudo`:

```bash
ssh -i /path/to/private-key USER@HOST_ADDRESS
```

Run **one** command on the VM. Each installs Tallpine, prerequisites, a persistent
user service, and a Cloudflare quick tunnel, then prints the connection details.

**Nightly users** (installs the newest published nightly without building from source):

```bash
bash -c 'set -o pipefail; sudo apt-get update && sudo apt-get install -y curl && curl -fsSL https://raw.githubusercontent.com/justvibedev/tallpine/main/scripts/bootstrap-self-hosted.sh | bash -s -- --nightly'
```

Use a nightly desktop app with a nightly host. Updating your laptop alone does
not update the VM. Until stable includes account linking, use this command for
the pair-once, automatic-discovery flow below. Nightlies are experimental.

**Testing an unreleased branch** (builds from that branch): replace
`codex/your-pr-branch` with the branch name published on GitHub.

```bash
SOURCE_REF=codex/your-pr-branch bash -c 'set -o pipefail; sudo apt-get update && sudo apt-get install -y curl && curl -fsSL "https://raw.githubusercontent.com/justvibedev/tallpine/${SOURCE_REF}/scripts/bootstrap-self-hosted.sh" | bash -s -- --source-ref "${SOURCE_REF}"'
```

**Stable users** (installs the latest published stable binary, not nightly):

```bash
bash -c 'set -o pipefail; sudo apt-get update && sudo apt-get install -y curl && curl -fsSL https://raw.githubusercontent.com/justvibedev/tallpine/main/scripts/bootstrap-self-hosted.sh | bash'
```

For a trusted LAN/VPN instead, append `--lan` to the script arguments
(`bash -s -- --lan` for the release command). This uses plaintext HTTP; never
expose the port publicly. The VM must stay powered on.

Re-run the command to upgrade without deleting sessions. The installer uses
`~/.tallpine/host`; it does not copy agent or GitHub credentials from your laptop.

## Check and connect

On the VM, get the current address and access key (keep it private):

```bash
~/.local/bin/tallpine remote-host status
```

If the HTTPS address is not ready, rerun the command after a few seconds.

For private Git clones, pushes, or PRs, authenticate on the VM as the Tallpine user:

```bash
gh auth login
gh auth setup-git
gh auth status
```

Pairing does not copy GitHub credentials from your laptop.

1. On the laptop, open **Settings → Connections → Other computers**, add the VM's name, exact
   `Address:` and `Access key:`. Alternatively, pair a phone once under
   **Settings → Connections → Phones**.
2. Repeat step 1 on each laptop or phone that should reach the VM; saved hosts
   are per device and are not synced between devices.
3. Use **Projects +** to clone or import a project on the VM. In desktop
   **Settings → Agents**, select the VM to install/sign in to an agent there.

A quick-tunnel address changes whenever the connector restarts; re-add the host
with the new `Address:` (see `tallpine remote-host status` on the VM) when that
happens.

## Status and other hosts

On the host, use `~/.local/bin/tallpine status` for daemon status,
`systemctl --user status tallpine-self-hosted.service --no-pager` for the service,
or `journalctl --user -u tallpine-self-hosted.service -n 100 --no-pager` for logs.
Run `~/.local/bin/tallpine remote-host disable` to stop remote access.

For macOS or another service manager, use the
[lower-level installer](../scripts/setup-self-hosted.sh); pass `--nightly` to
select the nightly channel. On macOS it installs
a LaunchAgent that runs only while the user is logged in; for a container,
use `--install-only` and supervise `tallpine daemon` yourself. Windows is not a
native host. Projects still need their own build dependencies on the host.

Removing a saved connection on desktop/mobile does not stop the host or its
sessions.

## Security and limits

- Tallpine's unauthenticated listener stays on `127.0.0.1`. The opt-in remote
  endpoint requires a password; `--lan` is plain HTTP, so use only a trusted
  LAN/VPN and never expose its port publicly.
- Each device holds its own copy of the computer's access key. Regenerating the VM
  access key revokes every previously paired device; re-pair them
  afterward.
- Host-ID checks prevent connecting to the wrong host, but not an active
  network attacker or a copied Tallpine data directory. Desktop access keys are stored
  in `~/.tallpine/remotes.json` (or `TALLPINE_DATA_DIR/remotes.json`) with owner-only access.
- Cloudflare terminates tunnel TLS and can see traffic. Quick tunnels have no
  uptime guarantee and change hostname on restart.
- Tallpine can edit host files; opening them in a client-side external editor needs
  a separate remote workspace connection.

## Mobile access is opt-in

The mobile app, its pairing endpoints, push notifications and tunnels are behind
a feature flag that is **off by default** (Settings > Mobile > "Enable mobile
access", or `tallpine remote-host enable` on a headless host). While it is off the
daemon runs no mobile listener, no `/api/v1/mobile`, `/api/v1/push` or
`/api/v1/endpoints` routes, no push relay and no tunnel. Turning it off stops
all of them immediately.

With it on, LAN pairing works as before. For reach beyond the LAN without a
hosted directory, set a **reachable address** (Settings > Mobile, or
`POST /api/v1/mobile/public-url`): a Tailscale `100.x`/MagicDNS URL or your own
reverse proxy. Pairing codes advertise it next to the LAN address. Plain `http`
is accepted only for private/Tailscale/local hosts; use `https` for anything on
the public internet. Paths are not supported (the app builds `scheme://host:port`).

A **Cloudflare quick tunnel** (trycloudflare.com) is a separate opt-in, off by
default, because Cloudflare terminates TLS for it and can see the traffic and
the pairing password.

Push notifications (Expo) are sent only while the flag is on, and say only
"session `<name>` needs input" or "session `<name>` finished": no code, diffs,
PR titles or message text. They still transit Expo's and the OS vendors' servers.

Remaining risks: the LAN listener and `http` base URLs are plaintext, so the
pairing password and API traffic can be read or replayed by anyone on that
network path; the password is stored unhashed in `~/.tallpine/mobile/config.json`
(0600); the Expo token, session name and host label are visible to Expo.
