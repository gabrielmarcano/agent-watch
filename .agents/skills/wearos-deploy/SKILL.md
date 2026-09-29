---
name: wearos-deploy
description: "Build, test and install the Wear OS app on the owner's Google Pixel Watch 2 over wireless adb, and read its logs. Use when asked to try, install, deploy or debug the Wear OS app on the watch."
---

# Build and install on the Pixel Watch 2

The user-facing steps (Firebase file, JDK, wireless `adb` pairing, install) are in `docs/GUIDE.md` § Build and Install the Wear OS App. This skill adds what an agent must do differently.

## Build and unit tests (no device needed)

```bash
cd wearos-app
export JAVA_HOME="$(/usr/libexec/java_home -v <JDK version, docs/GUIDE.md § Requirements>)"
./gradlew :app:testDebugUnitTest :app:lintDebug :app:assembleDebug
```

- `google-services.json` must exist in `wearos-app/app/`. It is the owner's file and git-ignored. If it is missing, **ask the owner**. Never create or commit it.
- One Gradle build at a time (`.agents/rules/wearos.md`).

## Emulator first

1. Use a round AVD named `aw-*` (192 dp like the Pixel Watch 2; e.g. `aw-wear-small-round`). If none exists, create one with that prefix (`system-images;android-34;android-wear;arm64-v8a`). Never touch AVDs you did not create.
2. A local relay (`agent-watch-relay serve` with `AW_LISTEN`, `AW_HOST_TOKEN` and `AW_DATA_DIR` in its environment, `contracts.md` §5) shows agents only when a host connects to it; the repo has no fake host. Only the debug build can reach a local `http://` relay (`src/debug/res/xml/network_security_config.xml`; from the emulator the Mac is `10.0.2.2`). An emulator paired with the production relay becomes a registered device: give the owner its name so he can revoke it.
3. Act only on `aw-sandbox` agents (the `capture-fixture` skill, steps 1–2), never on the owner's real ones.
4. Screenshots go to your scratchpad; anything committed shows sandbox agents only. Show the owner each changed screen or state before installing on the watch.

## Connect the watch (wireless debugging)

Install on the watch only after the owner agrees. There: never tap approvals of real agents, never swipe on the watch face (it dismisses notifications), and remember "Pin to tile" changes Quick Dictate's target.

`adb devices` should list the watch. If it does not, follow the GUIDE's wireless debugging steps, with two differences:

- **Pairing is the owner's** (once per watch, it needs the code on his watch): he runs `! adb pair <ip>:<pair-port> <code>` in this session.
- **`No route to host` while `ping` works:** macOS Local Network privacy. Only the system's own binaries reach the LAN from a process without that permission, and an agent running inside herdr has no app that can hold it. The owner starts the adb server from a regular terminal app, outside herdr, and allows Local Network when macOS asks:

  ```bash
  adb kill-server
  adb connect <ip>:<port>
  ```

  The agent's `adb` commands then use that server over `localhost`. Do not run `adb kill-server` from inside herdr afterwards: the new server would start without the permission.

## Install and run

Bump `WEAROS_VERSION_CODE` per `VERSIONS` for every build you install on the watch.

```bash
cd wearos-app
./gradlew :app:installDebug                       # or :app:installRelease to test R8/@Keep
adb shell am start -n com.gabriel.agentwatch/.MainActivity
```

**Release signing:** the release build is signed with the Mac's debug key (`~/.android/debug.keystore`), like the debug build. Either one installs over the other and keeps the pairing. An app signed with any other key fails with `INSTALL_FAILED_UPDATE_INCOMPATIBLE`: it must be uninstalled first, which deletes the pairing, and the watch must be paired again. Check before installing:

```bash
adb shell dumpsys package com.gabriel.agentwatch | grep -A1 signatures
```

The release build allows no cleartext HTTP, so it cannot reach a local `http://` relay; test it against HTTPS.

After installing, open the app once. A force-stop cancels the complication's tap action until the app refreshes it (seen on the emulator; an update likely does the same).

After installing on the owner's watch, update the **Deployed** line in `docs/STATUS.md`.

## Logs

```bash
adb logcat -c
adb logcat -s FCM:V RelayRepository:V NotifActionReceiver:V ApprovalNotifications:V Complication:V SurfaceUpdates:V MainActivity:V AndroidRuntime:E
```

## Reporting
- **Unit tests and build:** paste the Gradle output tail.
- **Behaviour on the watch:** only the owner can confirm what the screen shows and what the taps do. Give him a numbered list of what to check (the rows of `docs/phases/5-e2e.md` that your change touches, while that guide exists), and report his answers. **Never claim "works on the watch" without his confirmation.**
