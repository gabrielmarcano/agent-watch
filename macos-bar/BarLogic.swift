import Foundation

// Pure decision logic for the menu bar app: no AppKit, no global state.
// main.swift wires it to the UI.

// MARK: - CLI output

/// Mirrors `agent-watch-bridge status --json --local` (pkg/bridge LocalStatus).
/// Decoding is tolerant: a missing key keeps its default, so an older or
/// newer CLI never breaks the bar.
struct LocalStatus: Decodable, Equatable, Sendable {
    var installed = false
    var definitionError = ""
    var configured = false
    var configError = ""
    var running = false
    var stale = false
    var relayConnected = false
    var herdrOnline = false
    var agents = 0
    var blocked = 0
    var lastError = ""
    var relayError = ""
    var herdrError = ""
    var relayHost = ""
    var pid = 0
    var updatedAt = ""
    var ageSeconds = -1
    var version = ""
    var daemonVersion = ""
    var service = ""
    var definitionPath = ""
    var binary = ""
    var configPath = ""
    var stateDir = ""
    var statusPath = ""
    var logPath = ""

    enum CodingKeys: String, CodingKey {
        case installed, definitionError, configured, configError, running, stale
        case relayConnected, herdrOnline, agents, blocked, lastError, relayError, herdrError
        case relayHost, pid, updatedAt, ageSeconds, version, daemonVersion, service
        case definitionPath, binary, configPath, stateDir, statusPath, logPath
    }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func str(_ k: CodingKeys) -> String { ((try? c.decodeIfPresent(String.self, forKey: k)) ?? nil) ?? "" }
        func bool(_ k: CodingKeys) -> Bool { ((try? c.decodeIfPresent(Bool.self, forKey: k)) ?? nil) ?? false }
        func int(_ k: CodingKeys, _ d: Int = 0) -> Int { ((try? c.decodeIfPresent(Int.self, forKey: k)) ?? nil) ?? d }
        installed = bool(.installed)
        definitionError = str(.definitionError)
        configured = bool(.configured)
        configError = str(.configError)
        running = bool(.running)
        stale = bool(.stale)
        relayConnected = bool(.relayConnected)
        herdrOnline = bool(.herdrOnline)
        agents = int(.agents)
        blocked = int(.blocked)
        lastError = str(.lastError)
        relayError = str(.relayError)
        herdrError = str(.herdrError)
        relayHost = str(.relayHost)
        pid = int(.pid)
        updatedAt = str(.updatedAt)
        ageSeconds = int(.ageSeconds, -1)
        version = str(.version)
        daemonVersion = str(.daemonVersion)
        service = str(.service)
        definitionPath = str(.definitionPath)
        binary = str(.binary)
        configPath = str(.configPath)
        stateDir = str(.stateDir)
        statusPath = str(.statusPath)
        logPath = str(.logPath)
    }
}

/// Mirrors `agent-watch-bridge pair --json`.
struct PairInfo: Decodable, Equatable, Sendable {
    var code: String
    var expiresAt: String
    var expiresInSeconds: Int
    var relayHost: String
}

private func snakeCaseDecoder() -> JSONDecoder {
    let d = JSONDecoder()
    d.keyDecodingStrategy = .convertFromSnakeCase
    return d
}

func decodeLocalStatus(_ json: String) -> LocalStatus? {
    guard let data = json.data(using: .utf8) else { return nil }
    return try? snakeCaseDecoder().decode(LocalStatus.self, from: data)
}

func decodePairInfo(_ json: String) -> PairInfo? {
    guard let data = json.data(using: .utf8),
          let info = try? snakeCaseDecoder().decode(PairInfo.self, from: data),
          !info.code.isEmpty else { return nil }
    return info
}

// MARK: - State

/// What the bar shows. Every case must be recognisable at a glance.
enum BarState: Equatable, Sendable {
    case binaryMissing
    case statusUnavailable(String)
    case notConfigured(String)
    case notInstalled
    case stopped(String) // last_error, maybe empty
    case stale(Int)      // seconds since status.json was written
    case relayError(String)
    case connecting
    case herdrOffline(String)
    case connected
}

/// Derives the state from the last poll. A running bridge wins over config
/// problems (it keeps its loaded config until restarted).
func deriveState(binaryFound: Bool, status: LocalStatus?, pollError: String?) -> BarState {
    guard binaryFound else { return .binaryMissing }
    if let pollError { return .statusUnavailable(pollError) }
    guard let s = status else { return .statusUnavailable("no status yet") }
    if s.running {
        if s.stale { return .stale(s.ageSeconds) }
        if !s.relayConnected {
            let err = s.relayError.isEmpty ? s.lastError : s.relayError
            return err.isEmpty ? .connecting : .relayError(err)
        }
        if !s.herdrOnline { return .herdrOffline(s.herdrError) }
        return .connected
    }
    if !s.configured { return .notConfigured(s.configError) }
    if !s.installed { return .notInstalled }
    return .stopped(s.lastError)
}

// MARK: - Presentation

/// The small status circle drawn on the menu bar icon.
/// green: connected · yellow: running, not fully connected yet ·
/// red: should be working and is not · gray: off on purpose.
enum StatusDot: String, Equatable, Sendable {
    case green, yellow, red, gray
}

struct Presentation: Equatable, Sendable {
    var symbolName: String
    var dot: StatusDot
    var versions: [String]  // the "Versions" menu section
    var tooltip: String
    var headline: String
    var details: [String]
    var hint: String?       // what to do next, when the user must act outside the bar
    var canStart: Bool
    var canStop: Bool
    var canRestart: Bool
    var canPair: Bool
    var canOpenLogs: Bool
    var canRevealConfig: Bool
}

enum Symbols {
    static let connected = "applewatch.radiowaves.left.and.right"
    static let neutral = "applewatch"
    static let off = "applewatch.slash"
    static let problem = "exclamationmark.applewatch"
    static let all = [connected, neutral, off, problem]
}

/// The "Versions" menu section: this app, and the bridge (the running daemon's
/// version, else the CLI's). The bar reports bridge/relay health, never agents.
func versionLines(barVersion: String, status: LocalStatus?) -> [String] {
    var lines = ["Menu bar \(barVersion.isEmpty ? "unknown" : barVersion)"]
    if let s = status {
        let bridge = (s.running && !s.daemonVersion.isEmpty) ? s.daemonVersion : s.version
        if !bridge.isEmpty { lines.append("Bridge \(bridge)") }
    }
    return lines
}

let configureHint = "Configure it in a terminal: agent-watch-bridge configure --relay-url wss://<relay> --host-token <64 hex>"

func present(state: BarState, status: LocalStatus?, busy: String? = nil, barVersion: String = "") -> Presentation {
    let s = status ?? LocalStatus()
    let host = s.relayHost.isEmpty ? "the relay" : s.relayHost
    var p = Presentation(
        symbolName: Symbols.neutral, dot: .gray, versions: versionLines(barVersion: barVersion, status: status),
        tooltip: "", headline: "", details: [],
        hint: nil,
        canStart: false, canStop: s.installed, canRestart: s.installed && s.configured,
        canPair: s.configured, canOpenLogs: !s.logPath.isEmpty, canRevealConfig: !s.configPath.isEmpty
    )

    switch state {
    case .binaryMissing:
        p.symbolName = Symbols.problem
        p.dot = .red
        p.headline = "agent-watch-bridge not found"
        p.details = ["No installed LaunchAgent names it, and it is not next to this app."]
        p.hint = "Build it (make bridge) and run: bin/agent-watch-bridge start"
        p.canStop = false; p.canRestart = false; p.canPair = false
        p.canOpenLogs = false; p.canRevealConfig = false
    case .statusUnavailable(let err):
        p.symbolName = Symbols.problem
        p.dot = .red
        p.headline = "Bridge status unavailable"
        p.details = [err]
    case .notConfigured(let err):
        p.symbolName = Symbols.off
        p.headline = "Bridge not configured"
        p.details = err.isEmpty ? [] : [err]
        p.hint = configureHint
        p.canRestart = false
    case .notInstalled:
        p.symbolName = Symbols.off
        p.headline = "Bridge service not installed"
        p.details = ["Start installs the LaunchAgent and runs it."]
        p.canStart = true
    case .stopped(let err):
        p.symbolName = Symbols.off
        p.dot = err.isEmpty ? .gray : .red // an error means it failed to start
        p.headline = "Bridge stopped"
        p.details = err.isEmpty ? ["Your watch shows this Mac as offline."] : [err]
        p.canStart = true
    case .stale(let age):
        p.symbolName = Symbols.problem
        p.dot = .red
        p.headline = "Bridge not responding"
        p.details = [age >= 0 ? "No status update for \(age) s (it writes every 5 s)." : "Its status file has no valid timestamp."]
        p.hint = "Try Restart; if it persists, check the log."
    case .relayError(let err):
        p.symbolName = Symbols.problem
        p.dot = .red
        p.headline = "Relay error"
        p.details = [err, "Retrying \(host) in the background."]
    case .connecting:
        p.symbolName = Symbols.neutral
        p.dot = .yellow
        p.headline = "Connecting to \(host)…"
        p.details = []
    case .herdrOffline(let err):
        p.symbolName = Symbols.neutral
        p.dot = .yellow
        p.headline = "herdr is not running"
        p.details = [err.isEmpty ? "The bridge cannot reach the herdr socket." : err, "Connected to \(host)."]
    case .connected:
        p.symbolName = Symbols.connected
        p.dot = .green
        p.headline = "Connected to \(host)"
        p.details = []
    }

    var isStale = false
    if case .stale = state { isStale = true }
    if s.running, !isStale, !s.daemonVersion.isEmpty, !s.version.isEmpty, s.daemonVersion != s.version {
        p.details.append("The bridge runs \(s.daemonVersion); restart it to run \(s.version).")
    }
    p.canStart = p.canStart && s.configured && !s.running

    p.tooltip = tooltip(state: state, status: s)

    if let busy {
        p.headline = "\(busy)…"
        p.canStart = false; p.canStop = false; p.canRestart = false; p.canPair = false
    }
    return p
}

func tooltip(state: BarState, status s: LocalStatus) -> String {
    let host = s.relayHost.isEmpty ? "the relay" : s.relayHost
    switch state {
    case .connected:
        return "Agent Watch: connected to \(host)"
    case .connecting:
        return "Agent Watch: connecting to \(host)"
    case .relayError(let err):
        return "Agent Watch: relay error: \(err)"
    case .herdrOffline:
        return "Agent Watch: herdr is not running"
    case .stale:
        return "Agent Watch: bridge not responding"
    case .stopped:
        return "Agent Watch: bridge stopped"
    case .notInstalled:
        return "Agent Watch: bridge not installed"
    case .notConfigured:
        return "Agent Watch: bridge not configured"
    case .statusUnavailable(let err):
        return "Agent Watch: status unavailable: \(err)"
    case .binaryMissing:
        return "Agent Watch: agent-watch-bridge not found"
    }
}

// MARK: - Pairing

/// "417293" -> "4 1 7 · 2 9 3"
func spacedCode(_ code: String) -> String {
    let chars = Array(code)
    guard chars.count == 6 else { return code }
    return chars[0..<3].map(String.init).joined(separator: " ") + " · " + chars[3..<6].map(String.init).joined(separator: " ")
}

/// "Expires at 15:05 (in 5 min)." from the CLI's pair --json output.
func pairExpiryText(_ info: PairInfo, timeZone: TimeZone = .current) -> String {
    let secs = max(0, info.expiresInSeconds)
    let remaining = secs >= 60 ? "\((secs + 30) / 60) min" : "\(secs) s"
    let iso = ISO8601DateFormatter()
    guard let date = iso.date(from: info.expiresAt) else { return "Expires in \(remaining)." }
    let f = DateFormatter()
    f.locale = Locale(identifier: "en_US_POSIX")
    f.timeZone = timeZone
    f.dateFormat = "HH:mm"
    return "Expires at \(f.string(from: date)) (in \(remaining))."
}

// MARK: - Finding the bridge binary

let launchAgentLabel = "com.gabrielmarcano.agent-watch-bridge"

func launchAgentURL(home: URL) -> URL {
    home.appendingPathComponent("Library/LaunchAgents/\(launchAgentLabel).plist")
}

/// ProgramArguments[0] of a LaunchAgent plist.
func binaryFromLaunchAgent(_ data: Data) -> String? {
    guard let plist = try? PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any],
          let args = plist["ProgramArguments"] as? [Any],
          let first = args.first as? String, !first.isEmpty else { return nil }
    return first
}

/// The bridge binary: the one the installed LaunchAgent runs, else the one
/// next to this app bundle (build.sh puts both in bin/). Never a guess based
/// on the working directory or a hard-coded path.
func locateBridgeBinary(launchAgent: URL, appBundle: URL, isExecutable: (String) -> Bool) -> String? {
    if let data = try? Data(contentsOf: launchAgent),
       let bin = binaryFromLaunchAgent(data), isExecutable(bin) {
        return bin
    }
    let sibling = appBundle.deletingLastPathComponent().appendingPathComponent("agent-watch-bridge").path
    return isExecutable(sibling) ? sibling : nil
}

/// The environment for CLI calls: herdr's variables are removed so the CLI
/// resolves everything from the installed service definition.
func childEnvironment(_ base: [String: String]) -> [String: String] {
    base.filter { !$0.key.hasPrefix("HERDR_") }
}

// MARK: - Running the CLI

struct ProcessResult: Equatable, Sendable {
    var exitCode: Int32 = -1
    var stdout = ""
    var stderr = ""
    var timedOut = false
    var launchError: String?

    var ok: Bool { launchError == nil && !timedOut && exitCode == 0 }

    /// Text for an error alert.
    var failureDescription: String {
        if let launchError { return "Could not run the bridge CLI: \(launchError)" }
        var parts: [String] = []
        if timedOut { parts.append("The command timed out.") }
        let out = (stderr + (stderr.isEmpty || stdout.isEmpty ? "" : "\n") + stdout)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        if !out.isEmpty { parts.append(String(out.prefix(2000))) }
        if !timedOut { parts.append("Exit status \(exitCode).") }
        return parts.joined(separator: "\n\n")
    }
}

/// Collects a pipe's bytes from a background reader.
private final class DataSink: @unchecked Sendable {
    private let lock = NSLock()
    private var data = Data()
    func set(_ d: Data) { lock.lock(); data = d; lock.unlock() }
    var value: Data { lock.lock(); defer { lock.unlock() }; return data }
}

/// Runs a command and returns its output. Both pipes are drained on
/// background threads while the process runs, so output larger than the
/// pipe buffer (64 KB) cannot deadlock it; the process is terminated after
/// `timeout` seconds. Blocking: call it off the main thread.
func runProcess(executable: String, arguments: [String], timeout: TimeInterval,
                environment: [String: String]? = nil) -> ProcessResult {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: executable)
    process.arguments = arguments
    if let environment { process.environment = environment }
    process.standardInput = FileHandle.nullDevice
    let outPipe = Pipe()
    let errPipe = Pipe()
    process.standardOutput = outPipe
    process.standardError = errPipe

    let finished = DispatchSemaphore(value: 0)
    process.terminationHandler = { _ in finished.signal() }
    do {
        try process.run()
    } catch {
        return ProcessResult(launchError: error.localizedDescription)
    }
    // Our copies of the write ends must be closed for the readers to see EOF.
    try? outPipe.fileHandleForWriting.close()
    try? errPipe.fileHandleForWriting.close()

    let readers = DispatchGroup()
    let outSink = DataSink()
    let errSink = DataSink()
    for (handle, sink) in [(outPipe.fileHandleForReading, outSink), (errPipe.fileHandleForReading, errSink)] {
        readers.enter()
        DispatchQueue.global(qos: .utility).async {
            sink.set(handle.readDataToEndOfFile())
            readers.leave()
        }
    }

    var result = ProcessResult()
    if finished.wait(timeout: .now() + timeout) == .timedOut {
        result.timedOut = true
        process.terminate()
        if finished.wait(timeout: .now() + 2) == .timedOut {
            kill(process.processIdentifier, SIGKILL)
            _ = finished.wait(timeout: .now() + 2)
        }
    }
    _ = readers.wait(timeout: .now() + 2)
    result.exitCode = process.isRunning ? -1 : process.terminationStatus
    result.stdout = String(decoding: outSink.value, as: UTF8.self)
    result.stderr = String(decoding: errSink.value, as: UTF8.self)
    return result
}

/// Runs blocking work on a background queue and resumes with its result.
func runInBackground<T: Sendable>(_ work: @escaping @Sendable () -> T) async -> T {
    await withCheckedContinuation { continuation in
        DispatchQueue.global(qos: .userInitiated).async {
            continuation.resume(returning: work())
        }
    }
}
