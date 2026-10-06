import AppKit
import Foundation
import ServiceManagement

// Agent Watch menu bar companion. It shows the bridge's health and drives the
// agent-watch-bridge CLI; all decisions live in BarLogic.swift.
//
// Threading: the delegate is @MainActor and owns all state. Every file and
// process call runs off the main thread (runInBackground) and hands back a
// Sendable value that is applied on the main actor.

/// The menu bar icon with a small status circle in its bottom-right corner.
/// The symbol is tinted with the menu bar's text colour when it is drawn, so it
/// follows light and dark menu bars like a template image; the circle keeps its
/// own colour and is separated from the symbol by a thin transparent ring.
func statusIcon(symbolName: String, dot: StatusDot) -> NSImage? {
    guard let probe = NSImage(systemSymbolName: symbolName, accessibilityDescription: nil)
        ?? NSImage(systemSymbolName: Symbols.neutral, accessibilityDescription: nil) else { return nil }
    let diameter: CGFloat = 5
    let ring: CGFloat = 1.25
    let symbolSize = probe.size
    let size = NSSize(width: symbolSize.width + diameter / 2, height: symbolSize.height)
    let image = NSImage(size: size, flipped: false) { rect in
        guard let symbol = NSImage(systemSymbolName: symbolName, accessibilityDescription: nil)
            ?? NSImage(systemSymbolName: Symbols.neutral, accessibilityDescription: nil) else { return false }
        let symbolRect = NSRect(origin: .zero, size: symbolSize)
        symbol.draw(in: symbolRect)
        NSColor.labelColor.set()
        symbolRect.fill(using: .sourceAtop)

        let dotRect = NSRect(x: rect.maxX - diameter, y: 0, width: diameter, height: diameter)
        NSGraphicsContext.current?.compositingOperation = .clear
        NSBezierPath(ovalIn: dotRect.insetBy(dx: -ring, dy: -ring)).fill()
        NSGraphicsContext.current?.compositingOperation = .sourceOver
        switch dot {
        case .green: NSColor.systemGreen.setFill()
        case .yellow: NSColor.systemYellow.setFill()
        case .red: NSColor.systemRed.setFill()
        case .gray: NSColor.systemGray.setFill()
        }
        NSBezierPath(ovalIn: dotRect).fill()
        return true
    }
    image.isTemplate = false
    image.accessibilityDescription = "Agent Watch, status \(dot.rawValue)"
    return image
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate {
    private var statusItem: NSStatusItem?
    private let menu = NSMenu()

    // Built once; render() only updates titles, visibility and enablement.
    private let headlineItem = NSMenuItem(title: "", action: nil, keyEquivalent: "")
    private var detailItems: [NSMenuItem] = []
    private var versionItems: [NSMenuItem] = []
    private let barVersion = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
    private let hintItem = NSMenuItem(title: "", action: nil, keyEquivalent: "")
    private var startItem = NSMenuItem()
    private var stopItem = NSMenuItem()
    private var restartItem = NSMenuItem()
    private var pairItem = NSMenuItem()
    private var logsItem = NSMenuItem()
    private var configItem = NSMenuItem()
    private var loginItem = NSMenuItem()

    private var timer: Timer?
    private var pollInFlight = false
    private var binary: String?
    private var status: LocalStatus?
    private var pollError: String?
    private var busy: String?
    private var lastPresentation: Presentation?

    private let pollInterval: TimeInterval = 2.5
    private let statusTimeout: TimeInterval = 5
    private let actionTimeout: TimeInterval = 45

    func applicationDidFinishLaunching(_ notification: Notification) {
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        item.button?.imagePosition = .imageOnly
        statusItem = item
        buildMenu()
        item.menu = menu
        render()
        poll()

        // .common mode keeps polling while the menu is open (event tracking).
        let t = Timer(timeInterval: pollInterval, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.poll() }
        }
        RunLoop.main.add(t, forMode: .common)
        timer = t
    }

    // MARK: Menu

    private func buildMenu() {
        menu.autoenablesItems = false
        menu.delegate = self

        headlineItem.isEnabled = false
        menu.addItem(headlineItem)
        for _ in 0..<3 {
            let d = NSMenuItem(title: "", action: nil, keyEquivalent: "")
            d.isEnabled = false
            d.isHidden = true
            detailItems.append(d)
            menu.addItem(d)
        }
        hintItem.isEnabled = false
        hintItem.isHidden = true
        menu.addItem(hintItem)

        menu.addItem(.separator())
        startItem = addAction("Start Bridge", #selector(startBridge), "s")
        stopItem = addAction("Stop Bridge…", #selector(stopBridge), ".")
        restartItem = addAction("Restart Bridge", #selector(restartBridge), "r")
        menu.addItem(.separator())
        pairItem = addAction("Pair a Watch…", #selector(pairWatch), "p")
        menu.addItem(.separator())
        logsItem = addAction("Open Bridge Log", #selector(openLogs), "l")
        configItem = addAction("Show Configuration in Finder", #selector(revealConfig), ",")
        menu.addItem(.separator())
        let versionsHeader = NSMenuItem(title: "Versions", action: nil, keyEquivalent: "")
        versionsHeader.isEnabled = false
        menu.addItem(versionsHeader)
        for _ in 0..<3 { // menu bar, bridge, relay (versionLines)
            let v = NSMenuItem(title: "", action: nil, keyEquivalent: "")
            v.isEnabled = false
            v.isHidden = true
            versionItems.append(v)
            menu.addItem(v)
        }
        menu.addItem(.separator())
        loginItem = addAction("Open at Login", #selector(toggleOpenAtLogin), "")
        renderLoginItem()
        _ = addAction("Quit Agent Watch Menu", #selector(quit), "q")
    }

    private func addAction(_ title: String, _ action: Selector, _ key: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: action, keyEquivalent: key)
        item.target = self
        menu.addItem(item)
        return item
    }

    func menuWillOpen(_ menu: NSMenu) {
        poll() // refresh right away; items update in place while the menu is open
        renderLoginItem() // it can change in System Settings while the app runs
    }

    // MARK: Polling

    private var launchAgent: URL {
        launchAgentURL(home: FileManager.default.homeDirectoryForCurrentUser)
    }

    private var cliEnvironment: [String: String] {
        childEnvironment(ProcessInfo.processInfo.environment)
    }

    private func poll() {
        guard !pollInFlight else { return }
        pollInFlight = true
        let agentURL = launchAgent
        let bundleURL = Bundle.main.bundleURL
        let env = cliEnvironment
        let timeout = statusTimeout
        Task {
            let (bin, result) = await runInBackground { () -> (String?, ProcessResult?) in
                guard let bin = locateBridgeBinary(
                    launchAgent: agentURL, appBundle: bundleURL,
                    isExecutable: { FileManager.default.isExecutableFile(atPath: $0) }
                ) else { return (nil, nil) }
                return (bin, runProcess(executable: bin, arguments: ["status", "--json", "--local"],
                                        timeout: timeout, environment: env))
            }
            self.apply(binary: bin, result: result)
            self.pollInFlight = false
        }
    }

    private func apply(binary bin: String?, result: ProcessResult?) {
        binary = bin
        if bin == nil {
            status = nil
            pollError = nil
        } else if let result {
            if let decoded = decodeLocalStatus(result.stdout) {
                status = decoded
                pollError = nil
            } else {
                pollError = result.ok ? "unreadable output from agent-watch-bridge status" : result.failureDescription
            }
        }
        render()
    }

    // MARK: Rendering

    private func render() {
        let state = deriveState(binaryFound: binary != nil, status: status, pollError: pollError)
        let p = present(state: state, status: status, busy: busy, barVersion: barVersion)
        guard p != lastPresentation else { return }
        lastPresentation = p

        if let button = statusItem?.button {
            // Only the icon and its status dot: no text next to it.
            button.image = statusIcon(symbolName: p.symbolName, dot: p.dot)
            button.title = ""
            button.toolTip = p.tooltip
        }

        headlineItem.title = p.headline
        for (i, item) in detailItems.enumerated() {
            let text = i < p.details.count ? p.details[i] : ""
            item.title = "   " + text
            item.isHidden = text.isEmpty
            item.toolTip = i == 0 ? p.detailHelp : nil
        }
        for (i, item) in versionItems.enumerated() {
            let text = i < p.versions.count ? p.versions[i] : ""
            item.title = "   " + text
            item.isHidden = text.isEmpty
        }
        hintItem.title = "   " + (p.hint ?? "")
        hintItem.isHidden = p.hint == nil

        let running = status?.running ?? false
        startItem.isEnabled = p.canStart
        startItem.isHidden = running
        stopItem.isEnabled = p.canStop
        restartItem.isEnabled = p.canRestart
        pairItem.isEnabled = p.canPair
        logsItem.isEnabled = p.canOpenLogs
        configItem.isEnabled = p.canRevealConfig
    }

    // MARK: Actions

    @objc private func startBridge() { runCLI(["start"], busy: "Starting", failure: "Could not start the bridge") }

    @objc private func restartBridge() { runCLI(["restart"], busy: "Restarting", failure: "Could not restart the bridge") }

    @objc private func stopBridge() {
        let alert = NSAlert()
        alert.messageText = "Stop the bridge?"
        alert.informativeText = "Your watch will show this Mac as offline until you start it again."
        alert.addButton(withTitle: "Stop")
        alert.addButton(withTitle: "Cancel")
        activate()
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        runCLI(["stop"], busy: "Stopping", failure: "Could not stop the bridge")
    }

    /// Runs one CLI action off the main thread and reports any failure.
    private func runCLI(_ args: [String], busy label: String, failure: String) {
        guard let bin = binary, busy == nil else { return }
        busy = label
        render()
        let env = cliEnvironment
        let timeout = actionTimeout
        Task {
            let result = await runInBackground {
                runProcess(executable: bin, arguments: args, timeout: timeout, environment: env)
            }
            self.busy = nil
            if !result.ok {
                self.showAlert(failure, result.failureDescription)
            }
            self.render()
            self.poll()
        }
    }

    @objc private func pairWatch() {
        guard let bin = binary, busy == nil else { return }
        busy = "Requesting a pairing code"
        render()
        let env = cliEnvironment
        Task {
            let result = await runInBackground {
                runProcess(executable: bin, arguments: ["pair", "--json"], timeout: 20, environment: env)
            }
            self.busy = nil
            self.render()
            guard result.ok, let info = decodePairInfo(result.stdout) else {
                self.showAlert("Could not get a pairing code", result.failureDescription)
                return
            }
            self.showPairCode(info)
        }
    }

    private func showPairCode(_ info: PairInfo) {
        let alert = NSAlert()
        alert.messageText = "Watch pairing code"
        alert.informativeText = "Enter it in Agent Watch on your watch.\n\(pairExpiryText(info))"
        let code = NSTextField(labelWithString: spacedCode(info.code))
        code.font = NSFont.monospacedDigitSystemFont(ofSize: 30, weight: .semibold)
        code.isSelectable = true
        code.sizeToFit()
        alert.accessoryView = code
        alert.addButton(withTitle: "Done")
        alert.addButton(withTitle: "Copy Code")
        activate()
        if alert.runModal() == .alertSecondButtonReturn {
            copyConcealed(info.code)
        }
    }

    /// Copies the code marked as concealed, so clipboard managers skip it.
    private func copyConcealed(_ text: String) {
        let pb = NSPasteboard.general
        let concealed = NSPasteboard.PasteboardType("org.nspasteboard.ConcealedType")
        pb.clearContents()
        pb.declareTypes([.string, concealed], owner: nil)
        pb.setString(text, forType: .string)
        pb.setString(text, forType: concealed)
    }

    @objc private func openLogs() {
        guard let path = status?.logPath, !path.isEmpty else { return }
        guard FileManager.default.fileExists(atPath: path) else {
            showAlert("No bridge log yet", "\(path) does not exist. It appears once the bridge has run.")
            return
        }
        if !NSWorkspace.shared.open(URL(fileURLWithPath: path)) {
            showAlert("Could not open the bridge log", path)
        }
    }

    /// Reveals config.toml in Finder instead of opening the token file in an editor.
    @objc private func revealConfig() {
        guard let path = status?.configPath, !path.isEmpty else { return }
        let url = URL(fileURLWithPath: path)
        if FileManager.default.fileExists(atPath: path) {
            NSWorkspace.shared.activateFileViewerSelecting([url])
            return
        }
        let dir = url.deletingLastPathComponent()
        if FileManager.default.fileExists(atPath: dir.path) {
            NSWorkspace.shared.activateFileViewerSelecting([dir])
        }
        showAlert("No configuration yet", "\(path) does not exist.\n\n\(configureHelp)")
    }

    /// Registers or removes this app as a login item (SMAppService, macOS 13+).
    /// When macOS wants the owner's approval, it opens the Login Items settings.
    @objc private func toggleOpenAtLogin() {
        let service = SMAppService.mainApp
        if service.status == .requiresApproval {
            SMAppService.openSystemSettingsLoginItems()
            return
        }
        let wasEnabled = service.status == .enabled
        do {
            if wasEnabled {
                try service.unregister()
            } else {
                try service.register()
            }
        } catch {
            showAlert(wasEnabled ? "Could not stop opening at login" : "Could not open at login",
                      error.localizedDescription)
        }
        if service.status == .requiresApproval {
            SMAppService.openSystemSettingsLoginItems()
        }
        renderLoginItem()
    }

    /// Checked when enabled; a dash while macOS waits for approval in System Settings.
    private func renderLoginItem() {
        switch SMAppService.mainApp.status {
        case .enabled: loginItem.state = .on
        case .requiresApproval: loginItem.state = .mixed
        default: loginItem.state = .off
        }
    }

    @objc private func quit() {
        NSApp.terminate(nil)
    }

    // MARK: Alerts

    /// An accessory app's alerts open behind other windows unless it is activated first.
    private func activate() {
        if #available(macOS 14.0, *) {
            NSApp.activate()
        } else {
            NSApp.activate(ignoringOtherApps: true)
        }
    }

    private func showAlert(_ title: String, _ text: String) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = title
        alert.informativeText = text.isEmpty ? "No output." : text
        alert.addButton(withTitle: "OK")
        activate()
        alert.runModal()
    }
}

// Top-level code runs on the main thread; say so in both Swift 5 and 6 modes.
MainActor.assumeIsolated {
    let app = NSApplication.shared
    let delegate = AppDelegate()
    app.delegate = delegate
    app.setActivationPolicy(.accessory) // menu bar only, no Dock icon
    app.run() // never returns; keeps `delegate` alive (NSApplication holds it weakly)
}
