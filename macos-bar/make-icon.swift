// Draws the menu bar app's icon from the Wear OS launcher icon, so both
// apps share one artwork: the background and foreground vector drawables in
// wearos-app/app/src/main/res/drawable. Run by build.sh:
//
//   swift macos-bar/make-icon.swift <drawable dir> <out.iconset>
//
// It reads each <path>'s fillColor and pathData (M, L, C, H, V and Z, plus
// relative h and v; anything else fails the build) and draws them in a macOS
// icon tile: an 824-pt rounded square on a 1024 canvas, showing the 72-dp
// area an adaptive icon shows out of its 108-dp viewport.
import AppKit

struct VectorPath {
    var color: NSColor
    var path: NSBezierPath
}

func fail(_ msg: String) -> Never {
    FileHandle.standardError.write(("make-icon: " + msg + "\n").data(using: .utf8)!)
    exit(1)
}

func color(_ hex: String) -> NSColor {
    var h = hex.hasPrefix("#") ? String(hex.dropFirst()) : hex
    if h.count == 6 { h = "FF" + h }
    guard h.count == 8, let v = UInt32(h, radix: 16) else { fail("bad colour \(hex)") }
    return NSColor(srgbRed: CGFloat((v >> 16) & 0xFF) / 255, green: CGFloat((v >> 8) & 0xFF) / 255,
                   blue: CGFloat(v & 0xFF) / 255, alpha: CGFloat(v >> 24) / 255)
}

/// Parses M, L, C, H, V, h, v, Z path data in viewport units (y down).
func bezier(_ data: String) -> NSBezierPath {
    let scanner = Scanner(string: data)
    scanner.charactersToBeSkipped = CharacterSet(charactersIn: " ,\n\t")
    let p = NSBezierPath()
    func number() -> CGFloat {
        guard let n = scanner.scanDouble() else { fail("bad number in \(data)") }
        return n
    }
    func point() -> NSPoint {
        guard let x = scanner.scanDouble(), let y = scanner.scanDouble() else { fail("bad point in \(data)") }
        return NSPoint(x: x, y: y)
    }
    var command: Character?
    while !scanner.isAtEnd {
        if let c = scanner.scanCharacter(), c.isLetter {
            command = c
        } else {
            scanner.currentIndex = scanner.string.index(before: scanner.currentIndex)
        }
        switch command {
        case "M": p.move(to: point()); command = "L" // later pairs are line-tos
        case "L": p.line(to: point())
        case "C":
            let c1 = point(), c2 = point(), end = point()
            p.curve(to: end, controlPoint1: c1, controlPoint2: c2)
        case "H": p.line(to: NSPoint(x: number(), y: p.currentPoint.y))
        case "V": p.line(to: NSPoint(x: p.currentPoint.x, y: number()))
        case "h": let dx = number(); p.line(to: NSPoint(x: p.currentPoint.x + dx, y: p.currentPoint.y))
        case "v": let dy = number(); p.line(to: NSPoint(x: p.currentPoint.x, y: p.currentPoint.y + dy))
        case "Z", "z": p.close(); command = nil
        default: fail("unsupported path command \(String(describing: command)) in \(data)")
        }
    }
    return p
}

func paths(in file: String) -> [VectorPath] {
    guard let xml = try? String(contentsOfFile: file, encoding: .utf8) else { fail("cannot read \(file)") }
    let re = try! NSRegularExpression(pattern: #"<path[^>]*?android:fillColor="([^"]+)"[^>]*?android:pathData="([^"]+)""#,
                                      options: [.dotMatchesLineSeparators])
    let ns = xml as NSString
    let found = re.matches(in: xml, range: NSRange(location: 0, length: ns.length)).map {
        VectorPath(color: color(ns.substring(with: $0.range(at: 1))), path: bezier(ns.substring(with: $0.range(at: 2))))
    }
    if found.isEmpty { fail("no <path> with fillColor and pathData in \(file)") }
    return found
}

let args = CommandLine.arguments
guard args.count == 3 else { fail("usage: make-icon.swift <drawable dir> <out.iconset>") }
let layers = paths(in: args[1] + "/ic_launcher_background.xml") + paths(in: args[1] + "/ic_launcher_foreground.xml")

func render(size: Int) -> Data {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size, bitsPerSample: 8,
                               samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    let scale = CGFloat(size) / 1024
    let tile = NSRect(x: 100, y: 100, width: 824, height: 824)
    let shape = NSBezierPath(roundedRect: tile, xRadius: 185, yRadius: 185)
    let t = NSAffineTransform()
    t.scale(by: scale)
    t.concat()

    NSGraphicsContext.saveGraphicsState()
    let shadow = NSShadow()
    shadow.shadowColor = NSColor.black.withAlphaComponent(0.3)
    shadow.shadowOffset = NSSize(width: 0, height: -10)
    shadow.shadowBlurRadius = 20
    shadow.set()
    NSColor.black.setFill()
    shape.fill()
    NSGraphicsContext.restoreGraphicsState()

    shape.addClip()
    // The visible 72 dp (18...90) of the 108-dp viewport fill the tile; the
    // viewport's y axis points down.
    let v = NSAffineTransform()
    v.translateX(by: tile.minX, yBy: tile.maxY)
    v.scaleX(by: tile.width / 72, yBy: -tile.height / 72)
    v.translateX(by: -18, yBy: -18)
    for layer in layers {
        layer.color.setFill()
        (v.transform(layer.path)).fill()
    }
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

let out = URL(fileURLWithPath: args[2])
try? FileManager.default.createDirectory(at: out, withIntermediateDirectories: true)
for base in [16, 32, 128, 256, 512] {
    for (factor, suffix) in [(1, ""), (2, "@2x")] {
        let file = out.appendingPathComponent("icon_\(base)x\(base)\(suffix).png")
        do { try render(size: base * factor).write(to: file) } catch { fail("write \(file.path): \(error)") }
    }
}
