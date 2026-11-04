# Tallpine — Mobile

Expo (expo-router) mobile supervisor for Tallpine. Four tabs — Kanban, PRs,
Orchestrator, Settings — plus a Chat-first spawn flow, a native conversation surface,
the existing live terminal, and a preview browser. Chat sessions expose durable history,
streaming activity, approvals, provider controls, attachments, voice input, and a plain
worktree-shell escape hatch. It is a **thin client**: it talks to the Tallpine daemon running on your
computer through the endpoints advertised by the desktop pairing offer. It never runs agents itself.

> **Development builds only — Expo Go is not supported.** The app depends on native modules,
> so it must be compiled onto the device (`npx expo run:ios|run:android`). Expo Go can't load
> it; don't file bugs from it.

- [How the phone reaches your machine](#how-the-phone-reaches-your-machine)
- [Prerequisites](#prerequisites)
- [Install](#install)
- [Step 1 — Turn on Connect Mobile on the desktop](#step-1--turn-on-connect-mobile-on-the-desktop)
- [Step 2 — Build and install the dev build](#step-2--build-and-install-the-dev-build)
- [Step 3 — Pair the phone](#step-3--pair-the-phone)
- [Everyday dev loop](#everyday-dev-loop)
- [Troubleshooting](#troubleshooting)
- [Project layout](#project-layout)
- [Verify](#verify)

## How the phone reaches your machine

The daemon's primary listener is loopback-only (`127.0.0.1:3001`) and unauthenticated, so a
phone cannot reach it. To let a phone in, the desktop app opens a **second, opt-in LAN
listener** (default port **3011**) bound to `0.0.0.0`, protected by a rotating bearer
password, serving only the app API. It exists only while **Connect Mobile** is switched on
in the desktop app; switching it off closes the socket.

```
phone ──HTTP/WS── 0.0.0.0:3011   (LAN listener, bearer password, opt-in)
                       │
                 same daemon process
                       │
desktop/CLI ───── 127.0.0.1:3001 (loopback, no auth, unchanged)
```

LAN transport is **plaintext HTTP by design** and is for trusted home networks. A raw
Tailscale `100.x` endpoint is plaintext unless its pairing record has `secure: true`; iOS
blocks cleartext requests to `100.64.0.0/10`, so raw Tailscale is not an iOS path. The
desktop source contains an automatic secure-pairing attempt, but fresh configurations
report no secure-pairing availability while the mode is off, and current v2 endpoint data
do not establish a verified working path. Use a managed HTTPS endpoint whose pairing
record is marked `secure: true` for remote access. Background: [`docs/adr/0001-lan-listener-for-mobile.md`](../../docs/adr/0001-lan-listener-for-mobile.md).

## Forgetting a machine

Forgetting a machine removes its local pairing without waiting for network requests.
For identified machines, the phone first persists a remote-unpair cleanup job in
SecureStore. The daemon removes the phone's push registration when that job succeeds;
failed jobs are retried on launch, foregrounding, and every 30 seconds while the app is
active, even after the last machine is removed. Each retry checks the daemon's identity
before sending the saved credential. Credentials retained solely for cleanup are erased
after success, or when an old job is superseded by a deliberate re-pair.

An unreachable daemon can still send OS notifications until the phone reaches it and
remote cleanup succeeds. Already-sent notifications cannot be recalled. An old pairing
without a recorded host identity can only be forgotten locally: its remote registration
must be removed from the desktop, rather than sending credentials to an unverified address.

## Prerequisites

| For             | You need                                                                              |
| --------------- | ------------------------------------------------------------------------------------- |
| Everything      | Node 20+, and Tallpine running on your machine (desktop app, or the daemon from source)     |
| Phone ↔ machine | Same Wi-Fi network for LAN, or a reachable HTTPS endpoint for remote access         |
| iOS build       | macOS, Xcode 16+, an Apple ID (a free one gives a 7-day signing profile), a USB cable |
| Android build   | Android Studio (SDK + platform-tools for `adb`), a USB cable                          |

## Install

This package is **not** part of an npm workspace — install from inside it:

```bash
cd packages/mobile
npm install       # .npmrc sets legacy-peer-deps; postinstall runs patch-package
```

Two rules worth knowing before you fight an install:

- **Do not run `npm install --force` here.** It hoists SDK-incompatible transitive Expo deps
  and the app crashes on launch. Plain `npm install` in this directory is correct.
- `metro.config.js` pins `react` and `react-native` to this package's copies. Don't remove
  that — two React instances kill the app at startup with _"main has not been registered"_.

## Step 1 — Turn on Connect Mobile on the desktop

Nothing on the phone works until the desktop opens the LAN bridge. Do this first.

**1. Start the desktop app.** Either launch the packaged Tallpine app, or run it from source
(it starts its own daemon):

```bash
cd frontend && npm run dev      # Electron supervisor + daemon
```

**2. Open the pairing modal:** in the desktop app, **Settings → Connect Mobile**.

**3. Choose **Generate**.** The bridge binds immediately and the panel reveals the pairing
details:

- a v2 **QR code** with `hostId`, name, platform, every endpoint's `kind`, `host`, `port`,
  and `secure`, plus the bearer `token` in the link fragment. The QR or copied link is a
  secret;
- the current address and port, for example `192.168.1.84:3011`;
- the 8-character **connection password** (copyable);
- **Regenerate password**, which rotates the secret and invalidates every old token.

Leave the modal open — you scan that QR in step 3. Toggling **off** tears the bridge down,
so the phone goes offline until you turn it back on.

**Headless (daemon only, no Electron UI)?** Start the daemon and drive the same routes over
loopback:

```bash
cd backend && go run .

curl -X POST http://127.0.0.1:3001/api/v1/mobile/enable
curl -s      http://127.0.0.1:3001/api/v1/mobile/status    # includes current endpoint records
curl -X POST http://127.0.0.1:3001/api/v1/mobile/disable
```

Type the `host`/`port`/`password` from `status` into the app's Settings by hand (step 3).

## Step 2 — Build and install the dev build

You compile the app onto the device once; after that, JS changes hot-reload from Metro and
you only rebuild when native config changes. `ios/` and `android/` are **generated and
gitignored** — the run commands prebuild them for you. `ios.buildNumber` in `app.json` is tracked: bump it by 1 in the same PR as any change you install on a device or ship.

### On a cabled device (the main path)


**iOS, over the cable:**

1. Plug the iPhone in and tap **Trust This Computer**.
2. On the phone: **Settings → Privacy & Security → Developer Mode → On** (the phone reboots).
3. Build and install:

   ```bash
   npx expo run:ios --device      # pick your iPhone from the list
   ```

   If Xcode rejects the bundle identifier or team, open `ios/Orchestrator.xcworkspace`, select the
   **Orchestrator** target → **Signing & Capabilities**, choose your personal team and let Xcode manage
   signing, then re-run the command.

4. On first launch iOS asks for **Local Network** access. Allow it, or the app cannot reach
   the daemon on your LAN.

**Android, over the cable:**

1. On the phone, enable **Developer options** (tap Build number 7×), then **USB debugging**.
2. Plug in, accept the RSA fingerprint prompt, and confirm the device is visible:

   ```bash
   adb devices      # must list your device as "device", not "unauthorized"
   ```

3. Build and install:

   ```bash
   npx expo run:android --device
   ```

4. Let the phone reach Metro through the cable:

   ```bash
   adb reverse tcp:8081 tcp:8081
   ```

   Optional if the phone is on the same Wi-Fi, but it makes JS reloads immune to flaky Wi-Fi.
   It does **not** cover the daemon connection — that still goes over Wi-Fi (or Tailscale) to
   `host:3011`.

Android builds have cleartext HTTP **off** (`usesCleartextTraffic: false` in `app.json`); Android is
compile- and unit-test-only. On iOS, `NSAllowsLocalNetworking` in the prebuilt `Info.plist` only permits
cleartext to link-local, `.local`, and RFC 1918 (LAN) addresses. Tailscale's
`100.64.0.0/10` range is RFC 6598, so iOS blocks plaintext to it. The desktop source
contains an automatic secure-pairing attempt, but fresh configurations report no
secure-pairing availability while the mode is off, and current v2 endpoint records do not
establish a working Tailscale TLS route. Raw Tailscale is therefore not an iOS path. Use `secure: true` only for a genuinely HTTPS endpoint, and do not infer a physical
iOS or Android result from source inspection.

> **On `expo-dev-client`:** this package doesn't depend on it today, so the debug build
> connects straight to Metro and has no in-app launcher or URL switcher. If you want the
> launcher UI (scan a Metro QR from inside the app, switch bundler URLs), run
> `npx expo install expo-dev-client`, rebuild with `npx expo run:*`, and serve with
> `npx expo start --dev-client`.

### On a simulator / emulator

Same build commands without `--device`. The daemon host differs — a simulator isn't "on your
Wi-Fi" the way a phone is:

```bash
npx expo run:ios         # iOS Simulator    → daemon host 127.0.0.1
npx expo run:android     # Android emulator → daemon host 10.0.2.2
```

Port is `3011` either way. The pairing QR advertises all current endpoint records. If the
simulator or emulator cannot use one of them, type its host by hand in step 3.

## Step 3 — Pair the phone

During onboarding, tap **Pair a machine**, or tap **Scan pairing code** from an unpaired
screen, and grant camera access when asked. The pairing screen opens the scanner and offers
manual entry. The v2 offer carries the desktop identity, endpoint records, and bearer token.
The phone verifies the identity, races the endpoints, stores the winning desktop, and
reconnects without typing.

Manual entry, if you prefer (or for simulators / Tailscale):

| Field             | Value                                                                                |
| ----------------- | ------------------------------------------------------------------------------------ |
| **Host**          | Your machine's LAN IP (`ipconfig getifaddr en0` on macOS), or a reachable endpoint host |
| **API Port**      | `3011`, the Connect Mobile bridge, not the loopback `3001`                              |
| **Password**      | The 8-character connection password from the desktop panel                              |
| **Terminal Port** | Legacy and ignored. REST and `/mux` use the API port                                    |
| **Use TLS**       | Off for LAN. On only for a genuinely HTTPS endpoint marked `secure: true`               |

Tap **Connect**. A new manual connection tries the public identity endpoint first, but
ordinary probe errors do not stop it from sending the password to test the connection.
An incompatible contract version does stop it. Enter only a host you trust. The app
saves the connection after authentication succeeds. The password lives in the device
keystore, iOS Keychain or Android Keystore, never in AsyncStorage. A phone can keep
multiple desktops, and one desktop can authorize multiple phones. Rotating the desktop
password invalidates every old token.

## Everyday dev loop

With the dev build installed on the device, JS changes need no rebuild — just start Metro
and launch the app from the home screen:

```bash
npm start        # Metro; save a file and the phone hot-reloads
```

Rebuild with `npx expo run:ios|run:android` only when **native** config changes: `app.json`
plugins or permissions, native dependencies, `expo-build-properties`. After dependency
surgery, regenerate the native projects from scratch with `npx expo prebuild --clean`.

## Release build on the iPhone (USB only)

Tallpine ships to the owner's iPhone by cable and nowhere else. There is **no TestFlight,
no EAS Build/Submit, no over-the-air update and no App Store listing**: `expo-updates` is
not installed (`app.json` has no `updates.url`, owner or EAS project id) and the app has no
update or store-nudge code. Every change reaches the phone as a new signed build.

1. Bump `ios.buildNumber` in `app.json` by 1 (same PR as the change).
2. Generate the native project and install pods:

   ```bash
   npx expo prebuild --platform ios --clean --no-install
   (cd ios && pod install)
   ```

3. Build a signed Release app (bundle id `ee.justdev.tallpine.mobile`, team `YOUR_TEAM_ID`,
   automatic signing). Find the phone's id with `xcrun devicectl list devices`:

   ```bash
   xcodebuild -workspace ios/Tallpine.xcworkspace -scheme Tallpine -configuration Release \
     -destination 'id=<UDID>' -derivedDataPath ios/build -allowProvisioningUpdates \
     DEVELOPMENT_TEAM=YOUR_TEAM_ID CODE_SIGN_STYLE=Automatic build
   ```

4. Install and launch:

   ```bash
   xcrun devicectl device install app --device <UDID> ios/build/Products/Release-iphoneos/Tallpine.app
   xcrun devicectl device process launch --device <UDID> ee.justdev.tallpine.mobile
   ```

5. Check on the phone: launch, pair, open a chat. `npm run check` (typecheck, unit tests and
   the phone e2e against a real daemon) must pass first.

Push notifications use a separate Expo project as a relay only; until its project id is
set in `app.json` (`expo.extra.eas.projectId`) the app skips push registration before asking for
notification permission and Settings shows "Push not set up". There is no fallback id. After the
owner creates the Expo project and uploads the APNs key, set the id and reinstall the build.

## Troubleshooting

| Symptom                                           | Fix                                                                                                                                                          |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Crash at launch, _"main has not been registered"_ | Two React copies. Keep `metro.config.js` intact, reinstall with plain `npm install` (never `--force`), then `npx expo prebuild --clean`.                     |
| **Connect** fails, everything times out            | Connect Mobile is off on the desktop, wrong port (`3011`, not `3001`), phone on a different network, or the macOS firewall is blocking incoming connections. |
| 401 / invalid password                            | The password was regenerated on the desktop. Re-scan the QR.                                                                                                 |
| Locked out after repeated failures                | The bridge locks out a source after 5 failed attempts. Wait it out, or toggle Connect Mobile off and on.                                                     |
| `adb devices` shows `unauthorized`                | Re-accept the USB debugging prompt on the phone.                                                                                                             |
| iOS app installs, then closes immediately         | Untrusted developer profile: **Settings → General → VPN & Device Management → trust your Apple ID**.                                                         |
| App runs but can't reach the daemon (iOS)         | The Local Network prompt was denied: **Settings → Privacy & Security → Local Network → Orchestrator** → on.                                                            |
| Phone can't reach Metro                           | `adb reverse tcp:8081 tcp:8081` (Android), or `npx expo start --tunnel` (either platform).                                                                   |
| Terminal renders blank                            | The xterm WebView is patched via `patch-package`; confirm `postinstall` ran (`npx patch-package`).                                                           |

## Project layout

```
app/                 expo-router routes
  (tabs)/            Kanban (index), PRs, Orchestrator, Settings
  session/[id].tsx   persisted-mode router (native Chat or Terminal UI)
  shell/[handleId]   session-scoped worktree shell over the existing mux
  preview/[id]       unified live browser and generated app preview
  spawn.tsx          spawn flow
  pair.tsx           pairing-QR scanner
lib/
  api.ts             REST client for the daemon API
  chat/              paged/SSE conversation client and native Chat UI
  session/           existing TUI/xterm surface and terminal controls
  mux.ts             /mux WebSocket terminal transport
  config.ts          server config — password in SecureStore, the rest in AsyncStorage
  pairingCode.ts     pairing-QR payload parser
  pairFlow.ts        applies a scanned code — race endpoints, verify, store the host
  store.tsx          app state + connection polling
  theme.ts, ui.tsx   design primitives
scripts/             tallpine-phone-proxy.js — superseded by Connect Mobile, kept for reference
```

## Verify

```bash
npm run typecheck    # tsc --noEmit
npm test             # pure state/API/parser regression suite
npx expo export --platform ios
npx expo export --platform android
```
