import AppKit

/// The system switch. It shows the accent colour (blue by default) only
/// while the app is active: AppKit draws an inactive app's controls gray,
/// so the app activates while its menu is open (AppDelegate.menuWillOpen).
/// The first click counts even before that.
@MainActor
final class MenuSwitch: NSSwitch {
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }
}

/// The menu's first row: "Agent Watch", the state under it, and the on/off
/// switch that starts or stops the bridge service.
@MainActor
final class HeaderView: NSView {
    let title = NSTextField(labelWithString: "Agent Watch")
    let subtitle = NSTextField(labelWithString: "")
    let toggle = MenuSwitch()

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
