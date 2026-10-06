import AppKit

/// An on/off switch drawn in the system accent colour, like Tailscale's.
/// NSSwitch turns gray in a menu: an accessory app's menu window is never
/// key, and AppKit draws controls in inactive windows without their accent.
@MainActor
final class AccentSwitch: NSControl {
    var isOn = false {
        didSet { needsDisplay = true; setAccessibilityValue(isOn) }
    }
    override var isEnabled: Bool {
        didSet { needsDisplay = true }
    }

    override init(frame: NSRect) {
        super.init(frame: frame)
        setAccessibilityElement(true)
        setAccessibilityRole(.checkBox)
    }

    required init?(coder: NSCoder) { nil }

    override var intrinsicContentSize: NSSize { NSSize(width: 38, height: 22) }

    override func draw(_ dirtyRect: NSRect) {
        let track = bounds.insetBy(dx: 1, dy: 1)
        let radius = track.height / 2
        // Off: a translucent gray that reads on dark and light menus alike.
        let dark = effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
        let fill = isOn ? NSColor.controlAccentColor : NSColor(white: dark ? 1 : 0, alpha: 1)
        let fillAlpha: CGFloat = isOn ? 1 : (dark ? 0.22 : 0.14)
        fill.withAlphaComponent(fillAlpha * (isEnabled ? 1 : 0.45)).setFill()
        NSBezierPath(roundedRect: track, xRadius: radius, yRadius: radius).fill()

        let d = track.height - 4
        let knob = NSRect(x: isOn ? track.maxX - d - 2 : track.minX + 2, y: track.minY + 2, width: d, height: d)
        NSGraphicsContext.saveGraphicsState()
        let shadow = NSShadow()
        shadow.shadowColor = NSColor.black.withAlphaComponent(0.25)
        shadow.shadowOffset = NSSize(width: 0, height: -0.5)
        shadow.shadowBlurRadius = 1.5
        shadow.set()
        NSColor.white.withAlphaComponent(isEnabled ? 1 : 0.7).setFill()
        NSBezierPath(ovalIn: knob).fill()
        NSGraphicsContext.restoreGraphicsState()
    }

    override func mouseDown(with event: NSEvent) {
        flip()
    }

    override func accessibilityPerformPress() -> Bool {
        flip()
        return true
    }

    private func flip() {
        guard isEnabled else { return }
        isOn.toggle()
        sendAction(action, to: target)
    }
}

/// The menu's first row: "Agent Watch", the state under it, and the on/off
/// switch that starts or stops the bridge service (like Tailscale's).
@MainActor
final class HeaderView: NSView {
    let title = NSTextField(labelWithString: "Agent Watch")
    let subtitle = NSTextField(labelWithString: "")
    let toggle = AccentSwitch()

    init(target: AnyObject, action: Selector) {
        super.init(frame: NSRect(x: 0, y: 0, width: 280, height: 46))
        autoresizingMask = [.width]
        title.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .semibold)
        subtitle.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        subtitle.textColor = .secondaryLabelColor
        subtitle.lineBreakMode = .byTruncatingTail
        toggle.target = target
        toggle.action = action
        toggle.setAccessibilityLabel("Bridge on or off")

        let labels = NSStackView(views: [title, subtitle])
        labels.orientation = .vertical
        labels.alignment = .leading
        labels.spacing = 1
        for v in [labels, toggle] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            addSubview(v)
        }
        NSLayoutConstraint.activate([
            labels.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 20),
            labels.centerYAnchor.constraint(equalTo: centerYAnchor),
            labels.trailingAnchor.constraint(lessThanOrEqualTo: toggle.leadingAnchor, constant: -12),
            toggle.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -14),
            toggle.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    required init?(coder: NSCoder) { nil }
}
