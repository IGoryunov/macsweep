// Renders the macsweep app icon to a 1024x1024 PNG with CoreGraphics.
// Usage: swift packaging/icon.swift out.png
import Foundation
import CoreGraphics
import ImageIO
import UniformTypeIdentifiers

let size: CGFloat = 1024
let cs = CGColorSpaceCreateDeviceRGB()
let ctx = CGContext(data: nil, width: Int(size), height: Int(size), bitsPerComponent: 8, bytesPerRow: 0, space: cs, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
ctx.setAllowsAntialiasing(true)
ctx.setShouldAntialias(true)

func rgb(_ r: CGFloat, _ g: CGFloat, _ b: CGFloat, _ a: CGFloat = 1) -> CGColor { CGColor(colorSpace: cs, components: [r/255, g/255, b/255, a])! }

// macOS-style rounded square with a soft gradient.
let inset: CGFloat = size * 0.1
let rect = CGRect(x: inset, y: inset, width: size - 2*inset, height: size - 2*inset)
let path = CGPath(roundedRect: rect, cornerWidth: rect.width * 0.225, cornerHeight: rect.height * 0.225, transform: nil)
ctx.saveGState()
ctx.addPath(path); ctx.clip()
let grad = CGGradient(colorsSpace: cs, colors: [rgb(38, 52, 74), rgb(20, 27, 40)] as CFArray, locations: [0, 1])!
ctx.drawLinearGradient(grad, start: CGPoint(x: 0, y: size), end: CGPoint(x: 0, y: 0), options: [])
ctx.restoreGState()

// Sunburst rings: safe green, review amber, keep grey, neutral blue.
let c = CGPoint(x: size/2, y: size/2 - size*0.01)
func arc(_ r0: CGFloat, _ r1: CGFloat, _ a0: CGFloat, _ a1: CGFloat, _ color: CGColor) {
    let p = CGMutablePath()
    p.addArc(center: c, radius: r1, startAngle: a0, endAngle: a1, clockwise: false)
    p.addArc(center: c, radius: r0, startAngle: a1, endAngle: a0, clockwise: true)
    p.closeSubpath()
    ctx.addPath(p); ctx.setFillColor(color); ctx.fillPath()
    ctx.addPath(p); ctx.setStrokeColor(rgb(20, 27, 40)); ctx.setLineWidth(size*0.006); ctx.strokePath()
}
let d = CGFloat.pi / 180
let green = rgb(58, 166, 85), amber = rgb(217, 164, 0), grey = rgb(138, 143, 152)
let blue1 = rgb(91, 127, 166), blue2 = rgb(120, 156, 196)
// inner ring
arc(size*0.16, size*0.245, 90*d, 250*d, blue1)
arc(size*0.16, size*0.245, 250*d, 330*d, green)
arc(size*0.16, size*0.245, 330*d, 450*d, blue2)
// outer ring
arc(size*0.255, size*0.34, 90*d, 150*d, blue2)
arc(size*0.255, size*0.34, 150*d, 215*d, grey)
arc(size*0.255, size*0.34, 215*d, 250*d, blue1)
arc(size*0.255, size*0.34, 250*d, 300*d, green)
arc(size*0.255, size*0.34, 300*d, 335*d, green)
arc(size*0.255, size*0.34, 335*d, 385*d, amber)
arc(size*0.255, size*0.34, 385*d, 450*d, blue1)

// Centre disc with a check mark.
ctx.setFillColor(rgb(236, 239, 243))
ctx.fillEllipse(in: CGRect(x: c.x - size*0.13, y: c.y - size*0.13, width: size*0.26, height: size*0.26))
let check = CGMutablePath()
check.move(to: CGPoint(x: c.x - size*0.075, y: c.y + size*0.005))
check.addLine(to: CGPoint(x: c.x - size*0.02, y: c.y - size*0.05))
check.addLine(to: CGPoint(x: c.x + size*0.08, y: c.y + size*0.06))
ctx.addPath(check)
ctx.setStrokeColor(green); ctx.setLineWidth(size*0.035); ctx.setLineCap(.round); ctx.setLineJoin(.round)
ctx.strokePath()

let img = ctx.makeImage()!
let url = URL(fileURLWithPath: CommandLine.arguments[1]) as CFURL
let dest = CGImageDestinationCreateWithURL(url, UTType.png.identifier as CFString, 1, nil)!
CGImageDestinationAddImage(dest, img, nil)
CGImageDestinationFinalize(dest)
