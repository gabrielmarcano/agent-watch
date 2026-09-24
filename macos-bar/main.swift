import AppKit
import Foundation

// MARK: - Models

struct BridgeStatus: Decodable {
    let pid: Int
    let relayConnected: Bool
    let herdrOnline: Bool
    let agents: Int
    let lastError: String
    let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case pid
        case relayConnected = "relay_connected"
        case herdrOnline = "herdr_online"
        case agents
        case lastError = "last_error"
        case updatedAt = "updated_at"
    }
}

// MARK: - App Delegate

final class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private var timer: Timer?
    private var cachedStatus: BridgeStatus?
    private var isProcessRunning = false
    private var relayHost: String = ""

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Create the status bar item
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)

        if let button = statusItem.button {
            button.imagePosition = .imageLeading
            updateButton(status: nil, running: false)
        }

        buildMenu()
        loadRelayHost()
        refreshStatus()

        // Poll status every 2.5 seconds
        timer = Timer.scheduledTimer(withTimeInterval: 2.5, repeats: true) { [weak self] _ in
            self?.refreshStatus()
        }
    }

    // MARK: - Paths & Helpers

    private var homeDir: String {
        FileManager.default.homeDirectoryForCurrentUser.path
    }

    private var statusPath: String {
        if let env = ProcessInfo.processInfo.environment["HERDR_PLUGIN_STATE_DIR"] {
            return "\(env)/status.json"
        }
        return "\(homeDir)/.local/state/agent-watch/status.json"
    }

    private var configPath: String {
        if let env = ProcessInfo.processInfo.environment["HERDR_PLUGIN_CONFIG_DIR"] {
            return "\(env)/config.toml"
        }
        return "\(homeDir)/.config/herdr/plugins/config/herdr-agent-watch/config.toml"
    }

    private var logPath: String {
        "\(homeDir)/Library/Logs/agent-watch-bridge.log"
    }

    private func findBridgeBinary() -> String {
        // Check relative to current working directory or repo bin
        let cwd = FileManager.default.currentDirectoryPath
        let candidates = [
            "\(cwd)/bin/agent-watch-bridge",
            "\(homeDir)/Code/personal/agent-watch-herdr/bin/agent-watch-bridge",
            "/usr/local/bin/agent-watch-bridge"
        ]
        for candidate in candidates {
            if FileManager.default.isExecutableFile(atPath: candidate) {
                return candidate
            }
        }
        return "agent-watch-bridge"
    }

    private func runBridge(subcommand: String) -> (exitCode: Int32, output: String) {
        let binary = findBridgeBinary()
        let process = Process()
        process.executableURL = URL(fileURLWithPath: binary)
        process.arguments = [subcommand]

        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe

        do {
            try process.run()
            process.waitUntilExit()
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            let output = String(data: data, encoding: .utf8) ?? ""
            return (process.terminationStatus, output)
        } catch {
            return (-1, error.localizedDescription)
        }
    }

    // MARK: - Config & Status Reading

    private func loadRelayHost() {
        guard let content = try? String(contentsOfFile: configPath, encoding: .utf8) else { return }
        for line in content.components(separatedBy: .newlines) {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.hasPrefix("relay_url") {
                let parts = trimmed.components(separatedBy: "=")
                if parts.count >= 2 {
                    var val = parts[1].trimmingCharacters(in: .whitespaces)
                    val = val.replacingOccurrences(of: "\"", with: "")
                    if let u = URL(string: val), let host = u.host {
                        relayHost = host
                    } else {
                        relayHost = val
                    }
                }
            }
        }
    }

    private func refreshStatus() {
        var status: BridgeStatus?
        var running = false

        if let data = try? Data(contentsOf: URL(fileURLWithPath: statusPath)),
           let decoded = try? JSONDecoder().decode(BridgeStatus.self, from: data) {
            status = decoded
            // Verify PID is genuinely alive
            if decoded.pid > 0 && kill(pid_t(decoded.pid), 0) == 0 {
                running = true
            }
        }

        self.cachedStatus = status
        self.isProcessRunning = running

        DispatchQueue.main.async { [weak self] in
            guard let self = self else { return }
            self.updateButton(status: status, running: running)
            self.buildMenu()
        }
    }

    // MARK: - UI Updates

    private func updateButton(status: BridgeStatus?, running: Bool) {
        guard let button = statusItem.button else { return }

        // Use standard SF Symbol
        let symbolName = "applewatch.radiowaves.left.and.right"
        if let image = NSImage(systemSymbolName: symbolName, accessibilityDescription: "Agent Watch") {
            image.isTemplate = true
            button.image = image
        }

        if running, let st = status {
            if st.relayConnected {
                button.title = " \(st.agents)"
                button.toolTip = "Agent Watch: Connected to \(relayHost) (\(st.agents) agents active)"
            } else {
                button.title = " …"
                button.toolTip = "Agent Watch: Connecting to relay..."
            }
        } else {
            button.title = " ✕"
            button.toolTip = "Agent Watch: Stopped"
        }
    }

    private func buildMenu() {
        let menu = NSMenu()
        menu.autoenablesItems = false

        // Header: Status title
        let running = isProcessRunning
        let status = cachedStatus

        let statusItem: NSMenuItem
        if running, let st = status, st.relayConnected {
            statusItem = NSMenuItem(title: "🟢 Bridge Connected (\(st.agents) agents)", action: nil, keyEquivalent: "")
        } else if running {
            statusItem = NSMenuItem(title: "🟡 Bridge Running (connecting...)", action: nil, keyEquivalent: "")
        } else {
            statusItem = NSMenuItem(title: "⚪ Bridge Stopped", action: nil, keyEquivalent: "")
        }
        statusItem.isEnabled = false
        menu.addItem(statusItem)

        if !relayHost.isEmpty {
            let relayItem = NSMenuItem(title: "   Relay: \(relayHost)", action: nil, keyEquivalent: "")
            relayItem.isEnabled = false
            menu.addItem(relayItem)
        }

        if let st = status, running {
            let herdrItem = NSMenuItem(title: "   Herdr: \(st.herdrOnline ? "Online" : "Offline")", action: nil, keyEquivalent: "")
            herdrItem.isEnabled = false
            menu.addItem(herdrItem)
        }

        menu.addItem(NSMenuItem.separator())

        // Service Controls
        if running {
            let stopItem = NSMenuItem(title: "Stop Bridge", action: #selector(stopBridge), keyEquivalent: "s")
            stopItem.target = self
            menu.addItem(stopItem)

            let restartItem = NSMenuItem(title: "Restart Bridge", action: #selector(restartBridge), keyEquivalent: "r")
            restartItem.target = self
            menu.addItem(restartItem)
        } else {
            let startItem = NSMenuItem(title: "Start Bridge", action: #selector(startBridge), keyEquivalent: "s")
            startItem.target = self
            menu.addItem(startItem)
        }

        menu.addItem(NSMenuItem.separator())

        // Pairing
        let pairItem = NSMenuItem(title: "Pair Watch (Get Code)...", action: #selector(pairWatch), keyEquivalent: "p")
        pairItem.target = self
        pairItem.isEnabled = running
        menu.addItem(pairItem)

        menu.addItem(NSMenuItem.separator())

        // Diagnostics
        let logItem = NSMenuItem(title: "View Bridge Logs...", action: #selector(openLogs), keyEquivalent: "l")
        logItem.target = self
        menu.addItem(logItem)

        let configItem = NSMenuItem(title: "Open Configuration...", action: #selector(openConfig), keyEquivalent: "c")
        configItem.target = self
        menu.addItem(configItem)

        menu.addItem(NSMenuItem.separator())

        // Quit menu app
        let quitItem = NSMenuItem(title: "Quit Menu Bar App", action: #selector(quitApp), keyEquivalent: "q")
        quitItem.target = self
        menu.addItem(quitItem)

        self.statusItem.menu = menu
    }

    // MARK: - Actions

    @objc private func startBridge() {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            _ = self?.runBridge(subcommand: "start")
            Thread.sleep(forTimeInterval: 0.5)
            self?.refreshStatus()
        }
    }

    @objc private func stopBridge() {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            _ = self?.runBridge(subcommand: "stop")
            Thread.sleep(forTimeInterval: 0.5)
            self?.refreshStatus()
        }
    }

    @objc private func restartBridge() {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            _ = self?.runBridge(subcommand: "start")
            Thread.sleep(forTimeInterval: 0.5)
            self?.refreshStatus()
        }
    }

    @objc private func pairWatch() {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let res = self?.runBridge(subcommand: "pair") ?? (exitCode: -1, output: "Failed to run")
            DispatchQueue.main.async {
                self?.showPairAlert(output: res.output)
            }
        }
    }

    private func showPairAlert(output: String) {
        var code = ""
        for line in output.components(separatedBy: .newlines) {
            if line.contains("Pairing code:") {
                let parts = line.components(separatedBy: ":")
                if parts.count >= 2 {
                    code = parts[1].trimmingCharacters(in: .whitespaces)
                }
            }
        }

        let alert = NSAlert()
        if !code.isEmpty {
            alert.messageText = "Watch Pairing Code"
            alert.informativeText = "Enter this code on your watch app to complete pairing:\n\n\(code)\n\n(Expires in 5 minutes)"
            alert.alertStyle = .informational
            alert.addButton(withTitle: "Copy Code")
            alert.addButton(withTitle: "Done")

            // Clean 6 digits for clipboard
            let cleanCode = code.replacingOccurrences(of: "·", with: "").replacingOccurrences(of: " ", with: "")
            let response = alert.runModal()
            if response == .alertFirstButtonReturn {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(cleanCode, forType: .string)
            }
        } else {
            alert.messageText = "Pairing Output"
            alert.informativeText = output.isEmpty ? "No output from bridge." : output
            alert.alertStyle = .warning
            alert.addButton(withTitle: "OK")
            alert.runModal()
        }
    }

    @objc private func openLogs() {
        NSWorkspace.shared.open(URL(fileURLWithPath: logPath))
    }

    @objc private func openConfig() {
        NSWorkspace.shared.open(URL(fileURLWithPath: configPath))
    }

    @objc private func quitApp() {
        NSApp.terminate(nil)
    }
}

// MARK: - Main Entry Point

let app = NSApplication.shared
app.setActivationPolicy(.accessory) // Accessory policy = lives in menu bar, no dock icon
let delegate = AppDelegate()
app.delegate = delegate
app.run()
