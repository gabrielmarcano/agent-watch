---
name: wearos-deploy
description: "Build, test and install the Wear OS app on the owner's Google Pixel Watch 2 over wireless adb, and read its logs. Use when asked to try, install, deploy or debug the Wear OS app on the watch."
---

# Build and install on the Pixel Watch 2

## Build and unit tests (no device needed)

```bash
cd wearos-app
export JAVA_HOME="$(/usr/libexec/java_home -v 17)"   # any installed JDK 17 (Android Studio's bundled JBR works too)
./gradlew :app:testDebugUnitTest :app:assembleDebug
```

`google-services.json` must exist in `wearos-app/app/`. It is the owner's file and git-ignored. If it is missing, **ask the owner**. Never create or commit it.

## Connect the watch (wireless debugging)

`adb devices` should list the watch. If it does not:

1. **Pairing is needed only once per watch, and the owner does it.** On the watch: Settings → Developer options → Wireless debugging → **Pair new device**. It shows `IP:PORT` and a code. Then, on the Mac, the owner runs:

   ```bash
   ! adb pair <ip>:<pair-port> <code>
   ```

2. **Connect.** The connect port is shown on the Wireless debugging screen and differs from the pair port:

   ```bash
   adb connect <ip>:<port>
   adb devices     # the watch appears as <ip>:<port>  device
   ```

The watch and the Mac must be on the same Wi-Fi. The connect port changes after the watch reboots, so check the screen again.

**`No route to host` while `ping` works:** macOS Local Network privacy. Only the system's own binaries reach the LAN from a process without that permission, and an agent running inside herdr has no app that can hold it. The owner starts the adb server from a regular terminal app, outside herdr, and allows Local Network when macOS asks:

```bash
adb kill-server
adb connect <ip>:<port>
```

The agent's `adb` commands then use that server over `localhost`. Do not run `adb kill-server` from inside herdr afterwards: the new server would start without the permission.

## Install and run

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

## Logs

```bash
adb logcat -c
adb logcat -s AgentWatch:V FCM:V OkHttp:V AndroidRuntime:E
```

## Reporting
- **Unit tests and build:** paste the Gradle output tail.
- **Behaviour on the watch:** only the owner can confirm what the screen shows and what the taps do. Give him a numbered list of what to check (e.g. from `docs/phases/5-e2e.md`), and report his answers. **Never claim "works on the watch" without his confirmation.**
