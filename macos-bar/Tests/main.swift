import AppKit
import Foundation

// Test harness for macos-bar/BarLogic.swift (no XCTest, no Xcode project).
// Run it with `make bar-test`, which does:
//   swiftc -swift-version 6 macos-bar/BarLogic.swift macos-bar/Tests/main.swift -o <tmp>/bartests
//   <tmp>/bartests <repo>/bin/agent-watch-bridge <empty temp dir used as HOME>
//
// It never launches the app and never touches launchctl or the real HOME:
// the CLI checks run `status --json --local` and `pair --json` with HOME set
// to the throwaway directory (no config there, so pair fails before any
// network call). Without arguments the CLI checks are skipped.

var failures = 0
var checks = 0

@MainActor func check(_ cond: Bool, _ msg: String, line: Int = #line) {
    checks += 1
    if !cond {
        failures += 1
        print("FAIL (line \(line)): \(msg)")
    }
}

let args = CommandLine.arguments
let cli = args.count > 2 ? args[1] : ""
let fakeHome = args.count > 2 ? args[2] : ""

// Refuse to run the CLI against the real home directory.
if !cli.isEmpty {
    let realHome = String(cString: getpwuid(getuid()).pointee.pw_dir)
    let fake = URL(fileURLWithPath: fakeHome).resolvingSymlinksInPath().path
    let real = URL(fileURLWithPath: realHome).resolvingSymlinksInPath().path
    if fake == real || real.hasPrefix(fake + "/") {
        print("refusing to run: the fake HOME \(fakeHome) is (or contains) the real home \(realHome)")
        exit(2)
    }
}
let tmp = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("bartests-\(getpid())")
try? FileManager.default.createDirectory(at: tmp, withIntermediateDirectories: true)

// MARK: decoding

let full = """
{
  "installed": true, "definition_error": "", "configured": true, "config_error": "",
  "running": true, "stale": false, "relay_connected": true, "herdr_online": true,
  "agents": 9, "blocked": 2, "last_error": "", "relay_error": "", "herdr_error": "",
  "relay_host": "relay.example.com", "push_presence": true, "pid": 1159, "updated_at": "2026-09-25T14:51:06Z",
  "age_seconds": 2, "version": "0.3.0 (c8aa72e)", "daemon_version": "0.3.0 (c8aa72e)",
  "relay_version": "0.3.0 (5a32851)", "service": "launchd",
  "definition_path": "/Users/u/Library/LaunchAgents/com.gabrielmarcano.agent-watch-bridge.plist",
  "binary": "/Users/u/agent-watch/bin/agent-watch-bridge",
  "config_path": "/Users/u/.config/herdr/plugins/config/herdr-agent-watch/config.toml",
  "state_dir": "/Users/u/.local/state/herdr/plugins/herdr-agent-watch",
  "status_path": "/Users/u/.local/state/herdr/plugins/herdr-agent-watch/status.json",
  "log_path": "/Users/u/Library/Logs/agent-watch-bridge.log",
  "relay_status": {"host_online": true}
}
"""
let decoded = decodeLocalStatus(full)
check(decoded != nil, "full status decodes")
check(decoded?.blocked == 2 && decoded?.agents == 9 && decoded?.relayHost == "relay.example.com", "fields decode: \(String(describing: decoded))")
check(decoded?.logPath == "/Users/u/Library/Logs/agent-watch-bridge.log", "log path decodes")
check(decoded?.pushPresence == true, "push_presence decodes")
check(decoded?.relayVersion == "0.3.0 (5a32851)" && decoded?.daemonVersion == "0.3.0 (c8aa72e)",
      "versions decode: \(String(describing: decoded?.relayVersion))")
let sparse = decodeLocalStatus(#"{"running": true, "relay_connected": false}"#)
check(sparse?.running == true && sparse?.ageSeconds == -1 && sparse?.relayHost == "" && sparse?.relayVersion == "",
      "missing keys keep defaults")
check(decodeLocalStatus("not json") == nil, "garbage does not decode")

// MARK: the real CLI, with a throwaway HOME

if !cli.isEmpty {
    var env = ProcessInfo.processInfo.environment
    env["HOME"] = fakeHome
    env["HERDR_PLUGIN_STATE_DIR"] = "/should/be/stripped"
    let r = runProcess(executable: cli, arguments: ["status", "--json", "--local"], timeout: 5,
                       environment: childEnvironment(env))
    check(r.ok, "CLI status ran: \(r.failureDescription)")
    let st = decodeLocalStatus(r.stdout)
    check(st != nil, "CLI JSON decodes: \(r.stdout.prefix(200))")
    if let st {
        check(!st.installed && !st.configured && !st.running, "fresh home: not installed/configured/running")
        check(st.stateDir.hasPrefix(fakeHome) && !st.stateDir.contains("should/be/stripped"), "HERDR_* stripped, state dir from HOME: \(st.stateDir)")
        check(deriveState(binaryFound: true, status: st, pollError: nil) == .notConfigured(st.configError), "fresh home is notConfigured")
    }
    let pr = runProcess(executable: cli, arguments: ["pair", "--json"], timeout: 5, environment: childEnvironment(env))
    check(!pr.ok && pr.failureDescription.contains("configure"), "pair without config fails with a hint: \(pr.failureDescription)")
}

// MARK: state derivation

func st(_ mutate: (inout LocalStatus) -> Void) -> LocalStatus {
    var s = LocalStatus()
    s.installed = true; s.configured = true; s.running = true
    s.relayConnected = true; s.herdrOnline = true
    s.agents = 9; s.relayHost = "relay.example.com"; s.version = "0.2.0"; s.daemonVersion = "0.2.0"
    s.logPath = "/l.log"; s.configPath = "/c.toml"; s.ageSeconds = 2
    mutate(&s)
    return s
}

let cases: [(String, LocalStatus?, String?, Bool, BarState)] = [
    ("binary missing", nil, nil, false, .binaryMissing),
    ("poll error", nil, "timed out", true, .statusUnavailable("timed out")),
    ("connected", st { _ in }, nil, true, .connected),
    ("connecting", st { $0.relayConnected = false }, nil, true, .connecting),
    ("relay error", st { $0.relayConnected = false; $0.relayError = "relay rejected the host token (HTTP 401)"; $0.lastError = $0.relayError }, nil, true, .relayError("relay rejected the host token (HTTP 401)")),
    ("herdr offline", st { $0.herdrOnline = false; $0.herdrError = "herdr unreachable at /s" }, nil, true, .herdrOffline("herdr unreachable at /s")),
    ("stale", st { $0.stale = true; $0.ageSeconds = 40 }, nil, true, .stale(40)),
    ("stopped", st { $0.running = false; $0.lastError = "" }, nil, true, .stopped("")),
    ("stopped with reason", st { $0.running = false; $0.lastError = "config gone" }, nil, true, .stopped("config gone")),
    ("not installed", st { $0.running = false; $0.installed = false }, nil, true, .notInstalled),
    ("not configured", st { $0.running = false; $0.configured = false; $0.configError = "config missing" }, nil, true, .notConfigured("config missing")),
    ("running beats a broken config", st { $0.configured = false }, nil, true, .connected),
]
for (name, status, err, found, want) in cases {
    let got = deriveState(binaryFound: found, status: status, pollError: err)
    check(got == want, "\(name): got \(got), want \(want)")
}

// MARK: presentation

// The bar reports bridge/relay health only: never agents or blocked counts.
let connected2 = st { $0.blocked = 2 }
let p2 = present(state: .connected, status: connected2)
let p2Text = ([p2.headline, p2.tooltip, p2.hint ?? ""] + p2.details).joined(separator: " ").lowercased()
check(!p2Text.contains("block") && !p2Text.contains("agents") && !p2Text.contains("waiting"), "no agent/blocked talk: \(p2Text)")
check(p2.headline == "Connected to relay.example.com", "connected headline: \(p2.headline)")

let p0 = present(state: .connected, status: st { _ in })
check(p0.tooltip == "Agent Watch: connected to relay.example.com", "tooltip: \(p0.tooltip)")
check(p0.canStop && p0.canRestart && p0.canPair && !p0.canStart, "running: stop/restart/pair, no start")

let pNC = present(state: .notConfigured("config missing"), status: st { $0.running = false; $0.configured = false })
check(!pNC.canStart && !pNC.canPair && !pNC.canRestart, "not configured: Start/Pair/Restart disabled")
check(pNC.hint?.contains("configure") == true, "not configured: hint to run configure")

let stoppedStatus = st { $0.running = false }
let pStopped = present(state: .stopped(""), status: stoppedStatus)
check(pStopped.canStart && pStopped.canPair, "stopped: Start and Pair (pair does not need the bridge)")

let pNI = present(state: .notInstalled, status: st { $0.running = false; $0.installed = false })
check(pNI.canStart && !pNI.canStop && !pNI.canRestart, "not installed: Start only")

let pErr = present(state: .relayError("relay rejected the host token (HTTP 401)"), status: st { $0.relayConnected = false })
check(pErr.details.first == "The relay rejected the host token" && pErr.detailHelp == "relay rejected the host token (HTTP 401)", "relay error: summary in the menu, last_error as its tooltip: \(pErr.details)")

let pMissing = present(state: .binaryMissing, status: nil)
check(!pMissing.canStart && !pMissing.canStop && !pMissing.canPair && pMissing.hint != nil, "binary missing: nothing to run")

let pBusy = present(state: .connected, status: connected2, busy: "Restarting")
check(!pBusy.canStart && !pBusy.canStop && !pBusy.canRestart && !pBusy.canPair && pBusy.headline == "Restarting…", "busy disables actions")

let pOld = present(state: .connected, status: st { $0.daemonVersion = "0.1.0" })
check(pOld.details.contains { $0.contains("restart for 0.2.0") }, "version mismatch hint")

// The spec's states must be distinguishable at a glance (icon + title).
let glance: [(String, BarState, LocalStatus)] = [
    ("not installed", .notInstalled, st { $0.running = false; $0.installed = false }),
    ("not configured", .notConfigured(""), st { $0.running = false; $0.configured = false }),
    ("stopped", .stopped(""), stoppedStatus),
    ("connected", .connected, st { _ in }),
    ("connecting", .connecting, st { $0.relayConnected = false }),
    ("relay error", .relayError("x"), st { $0.relayConnected = false }),
    ("herdr offline", .herdrOffline(""), st { $0.herdrOnline = false }),
    ("stale", .stale(40), st { $0.stale = true }),
]
var seen: [String: String] = [:]
for (name, state, status) in glance {
    let p = present(state: state, status: status)
    let key = p.headline
    if let other = seen[key] { check(false, "\(name) reads like \(other) in the menu: \(key)") }
    seen[key] = name
}

// Every symbol exists on this system.
for name in Symbols.all {
    check(NSImage(systemSymbolName: name, accessibilityDescription: nil) != nil, "SF Symbol \(name) exists")
}

// MARK: the on/off switch

// The switch is on while the bridge runs, and flips the service: on starts
// it, off stops it. It is disabled while an action runs or when the flip
// would fail (not configured, binary missing).
let switchCases: [(String, BarState, LocalStatus?, String?, Bool, Bool)] = [
    ("connected", .connected, st { _ in }, nil, true, true),
    ("relay error", .relayError("x"), st { $0.relayConnected = false }, nil, true, true),
    ("stale still runs", .stale(40), st { $0.stale = true }, nil, true, true),
    ("stopped", .stopped(""), st { $0.running = false }, nil, false, true),
    ("not installed", .notInstalled, st { $0.running = false; $0.installed = false }, nil, false, true),
    ("not configured", .notConfigured("c"), st { $0.running = false; $0.configured = false }, nil, false, false),
    ("binary missing", .binaryMissing, nil, nil, false, false),
    ("busy", .connected, st { _ in }, "Stopping", true, false),
]
for (name, state, status, busy, on, enabled) in switchCases {
    let p = present(state: state, status: status, busy: busy)
    check(p.switchOn == on && p.switchEnabled == enabled, "switch \(name): on \(p.switchOn) enabled \(p.switchEnabled), want \(on) \(enabled)")
}

// MARK: Only Notify When Away

// Checked while the config has push_presence on; it can be flipped whenever
// the bridge is configured, running or not, except while an action runs.
let awayCases: [(String, LocalStatus?, String?, Bool, Bool)] = [
    ("on", st { $0.pushPresence = true }, nil, true, true),
    ("off", st { _ in }, nil, false, true),
    ("stopped", st { $0.running = false; $0.pushPresence = true }, nil, true, true),
    ("not configured", st { $0.running = false; $0.configured = false }, nil, false, false),
    ("no status", nil, nil, false, false),
    ("busy", st { $0.pushPresence = true }, "Saving", true, false),
]
for (name, status, busy, on, enabled) in awayCases {
    let state = status == nil ? BarState.binaryMissing : deriveState(binaryFound: true, status: status, pollError: nil)
    let p = present(state: state, status: status, busy: busy)
    check(p.awayOnlyOn == on && p.canToggleAwayOnly == enabled, "away-only \(name): on \(p.awayOnlyOn) enabled \(p.canToggleAwayOnly), want \(on) \(enabled)")
}

// MARK: short error lines

// The menu is as wide as its longest line: raw Go errors never go in it.
let dialTimeout = "relay unreachable: failed to WebSocket dial: failed to send handshake request: Get \"https://relay.example.com/v1/host\": context deadline exceeded"
let errorCases: [(String, String)] = [
    (dialTimeout, "Relay unreachable (timed out)"),
    ("relay unreachable: failed to WebSocket dial: failed to send handshake request: Get \"https://relay.example.com/v1/host\": dial tcp 1.2.3.4:443: connect: connection refused", "Relay unreachable (connection refused)"),
    ("relay unreachable: failed to WebSocket dial: failed to send handshake request: Get \"https://relay.example.com/v1/host\": dial tcp: lookup relay.example.com: no such host", "Relay unreachable (host not found)"),
    ("relay unreachable: failed to WebSocket dial: Get \"https://relay.example.com/v1/host\": dial tcp 1.2.3.4:443: connect: network is unreachable", "Relay unreachable (no network)"),
    ("relay unreachable: failed to WebSocket dial: Get \"https://relay.example.com/v1/host\": tls: failed to verify certificate: x509: certificate has expired", "Relay unreachable (TLS certificate)"),
    ("relay unreachable: failed to WebSocket dial: expected handshake response status code 101 but got 502", "Relay unreachable"),
    ("relay connection lost: failed to get reader: failed to read frame header: EOF", "Connection to the relay lost"),
    ("relay rejected the host token (HTTP 401); re-run configure with the relay's AW_HOST_TOKEN", "The relay rejected the host token"),
    ("herdr unreachable at /s", "herdr unreachable at /s"),
    ("config gone", "config gone"),
]
for (raw, want) in errorCases {
    let got = errorSummary(raw)
    check(got == want, "errorSummary: got \(got), want \(want) for \(raw.prefix(60))")
}
let longUnknown = String(repeating: "word ", count: 40)
let longSummary = errorSummary(longUnknown)
check(longSummary.count <= maxMenuLine && longSummary.hasSuffix("…"), "a long unknown error is cut: \(longSummary)")
check(errorSummary("first line\nsecond line") == "first line", "only the first line")

let pRelay = present(state: .relayError(dialTimeout), status: st { $0.relayConnected = false; $0.relayError = dialTimeout })
check(pRelay.details.first == "Relay unreachable (timed out)", "relay error detail is the summary: \(pRelay.details)")
check(pRelay.detailHelp == dialTimeout, "the raw error is the detail's tooltip")
check(pRelay.tooltip == "Agent Watch: Relay unreachable (timed out)", "icon tooltip is short: \(pRelay.tooltip)")

// No menu line is longer than maxMenuLine, whatever the error.
let longStates: [BarState] = [
    .relayError(dialTimeout), .stopped(dialTimeout), .statusUnavailable(dialTimeout),
    .notConfigured(dialTimeout), .herdrOffline(dialTimeout + dialTimeout),
]
for state in longStates {
    let p = present(state: state, status: st { $0.relayConnected = false; $0.running = false })
    for line in [p.headline, p.hint ?? ""] + p.details {
        check(line.count <= maxMenuLine, "\(state): line too long (\(line.count)): \(line)")
    }
}

// MARK: pairing

let pair = decodePairInfo(#"{"code":"417293","expires_at":"2026-09-25T15:05:00Z","expires_in_seconds":300,"relay_host":"relay.example.com"}"#)
check(pair?.code == "417293" && pair?.expiresInSeconds == 300, "pair JSON decodes")
check(spacedCode("417293") == "4 1 7 · 2 9 3", "spaced code")
if let pair {
    let text = pairExpiryText(pair, timeZone: TimeZone(identifier: "UTC")!)
    check(text == "Expires at 15:05 (in 5 min).", "expiry text: \(text)")
}
check(decodePairInfo(#"{"code":""}"#) == nil, "empty code rejected")

// MARK: finding the binary

let exe = tmp.appendingPathComponent("bin/agent-watch-bridge")
try? FileManager.default.createDirectory(at: exe.deletingLastPathComponent(), withIntermediateDirectories: true)
FileManager.default.createFile(atPath: exe.path, contents: Data("#!/bin/sh\n".utf8), attributes: [.posixPermissions: 0o755])
let bundle = tmp.appendingPathComponent("bin/Agent Watch.app")
let plistURL = tmp.appendingPathComponent("agent.plist")
let isExec: (String) -> Bool = { FileManager.default.isExecutableFile(atPath: $0) }

let other = tmp.appendingPathComponent("plugin/agent-watch-bridge")
try? FileManager.default.createDirectory(at: other.deletingLastPathComponent(), withIntermediateDirectories: true)
FileManager.default.createFile(atPath: other.path, contents: Data("#!/bin/sh\n".utf8), attributes: [.posixPermissions: 0o755])
let plist: [String: Any] = ["Label": launchAgentLabel, "ProgramArguments": [other.path, "run", "--config", "/c.toml"]]
let plistData = try! PropertyListSerialization.data(fromPropertyList: plist, format: .xml, options: 0)
try! plistData.write(to: plistURL)
check(locateBridgeBinary(launchAgent: plistURL, appBundle: bundle, isExecutable: isExec) == other.path, "binary from the LaunchAgent")

try? FileManager.default.removeItem(at: other)
check(locateBridgeBinary(launchAgent: plistURL, appBundle: bundle, isExecutable: isExec) == exe.path, "missing plist binary: sibling of the app")
try? FileManager.default.removeItem(at: exe)
check(locateBridgeBinary(launchAgent: plistURL, appBundle: bundle, isExecutable: isExec) == nil, "nothing found: nil (no relative fallback)")

check(childEnvironment(["HERDR_SOCKET_PATH": "x", "HERDR_PLUGIN_STATE_DIR": "y", "PATH": "/bin"]) == ["PATH": "/bin"], "HERDR_* stripped")

// MARK: process runner

let big = runProcess(executable: "/bin/sh", arguments: ["-c", "head -c 300000 /dev/zero | tr '\\0' a; echo done >&2"], timeout: 10)
check(big.ok && big.stdout.count == 300000 && big.stderr == "done\n", "300 KB of output without a pipe deadlock (got \(big.stdout.count) bytes, \(big.failureDescription))")

let t0 = Date()
let slow = runProcess(executable: "/bin/sleep", arguments: ["5"], timeout: 0.3)
check(slow.timedOut && !slow.ok, "timeout is reported")
check(Date().timeIntervalSince(t0) < 3, "timeout returns promptly: \(Date().timeIntervalSince(t0)) s")

let failing = runProcess(executable: "/bin/sh", arguments: ["-c", "echo boom >&2; exit 3"], timeout: 5)
check(!failing.ok && failing.exitCode == 3 && failing.failureDescription.contains("boom") && failing.failureDescription.contains("Exit status 3"), "failure keeps output: \(failing.failureDescription)")

let missing = runProcess(executable: "/nonexistent/agent-watch-bridge", arguments: [], timeout: 5)
check(missing.launchError != nil && !missing.ok, "launch error is reported")

// runInBackground from an async context
let sem = DispatchSemaphore(value: 0)
let box = ResultBox()
Task.detached {
    let v = await runInBackground { 21 * 2 }
    box.set(v)
    sem.signal()
}
_ = sem.wait(timeout: .now() + 5)
check(box.get() == 42, "runInBackground returns the value")

// Versions section: the bar's own version and the bridge's (daemon when running, else the CLI).
let vRun = versionLines(barVersion: "0.2.1", status: st { $0.version = "0.2.1"; $0.daemonVersion = "0.2.1-abc1234" })
check(vRun == ["Menu bar 0.2.1", "Bridge 0.2.1-abc1234"], "versions while running: \(vRun)")
let vOff = versionLines(barVersion: "0.2.1", status: st { $0.running = false; $0.daemonVersion = ""; $0.version = "0.2.1" })
check(vOff == ["Menu bar 0.2.1", "Bridge 0.2.1"], "versions while stopped: \(vOff)")
check(versionLines(barVersion: "", status: nil) == ["Menu bar unknown"], "no bridge status, no bundle version")
check(present(state: .connected, status: st { $0.daemonVersion = "0.2.1-abc1234" }, barVersion: "0.2.1").versions
      == ["Menu bar 0.2.1", "Bridge 0.2.1-abc1234"], "present carries the versions")

// The relay's version, when the running bridge knows it.
let vRelay = versionLines(barVersion: "0.3.0", status: st {
    $0.version = "0.3.0 (c8aa72e)"; $0.daemonVersion = "0.3.0 (c8aa72e)"; $0.relayVersion = "0.3.0 (5a32851)"
})
check(vRelay == ["Menu bar 0.3.0", "Bridge 0.3.0 (c8aa72e)", "Relay 0.3.0 (5a32851)"], "versions with the relay: \(vRelay)")
let vRelayDown = versionLines(barVersion: "0.3.0", status: st {
    $0.relayConnected = false; $0.daemonVersion = "0.3.0 (c8aa72e)"; $0.relayVersion = "0.3.0 (5a32851)"
})
check(vRelayDown.last == "Relay 0.3.0 (5a32851)", "relay down: last known relay version kept: \(vRelayDown)")
let vNoRelay = versionLines(barVersion: "0.3.0", status: st { $0.daemonVersion = "0.3.0 (c8aa72e)"; $0.relayVersion = "" })
check(vNoRelay == ["Menu bar 0.3.0", "Bridge 0.3.0 (c8aa72e)"], "relay version unknown: no relay line: \(vNoRelay)")
let vStoppedRelay = versionLines(barVersion: "0.3.0", status: st {
    $0.running = false; $0.daemonVersion = ""; $0.version = "0.3.0 (c8aa72e)"; $0.relayVersion = "0.3.0 (5a32851)"
})
check(vStoppedRelay == ["Menu bar 0.3.0", "Bridge 0.3.0 (c8aa72e)"], "bridge stopped: no relay line: \(vStoppedRelay)")
check(present(state: .connected, status: st { $0.relayVersion = "0.3.0 (5a32851)" }, barVersion: "0.3.0").versions.count == 3,
      "present carries the relay line")

// Status dot: green connected, yellow on its way, red broken, gray off on purpose.
let dotCases: [(BarState, StatusDot, String)] = [
    (.connected, .green, "connected"),
    (.connecting, .yellow, "connecting"),
    (.herdrOffline(""), .yellow, "herdr offline"),
    (.relayError("401 unauthorized"), .red, "relay error"),
    (.stale(40), .red, "stale status"),
    (.stopped("load config: missing host_token"), .red, "failed start"),
    (.statusUnavailable("timeout"), .red, "status unavailable"),
    (.binaryMissing, .red, "binary missing"),
    (.stopped(""), .gray, "stopped on purpose"),
    (.notInstalled, .gray, "not installed"),
    (.notConfigured(""), .gray, "not configured"),
]
for (state, want, name) in dotCases {
    let got = present(state: state, status: st { _ in }).dot
    check(got == want, "dot for \(name): got \(got), want \(want)")
}
check(present(state: .connected, status: st { $0.blocked = 2 }).dot == .green, "blocked agents keep the green dot")

try? FileManager.default.removeItem(at: tmp)
print("\(checks) checks, \(failures) failures")
exit(failures == 0 ? 0 : 1)

final class ResultBox: @unchecked Sendable {
    private let lock = NSLock()
    private var v = 0
    func set(_ x: Int) { lock.lock(); v = x; lock.unlock() }
    func get() -> Int { lock.lock(); defer { lock.unlock() }; return v }
}
