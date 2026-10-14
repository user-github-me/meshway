#!/usr/bin/swift
import AppKit
import Foundation
let output = URL(fileURLWithPath: CommandLine.arguments[1])
try FileManager.default.createDirectory(at: output, withIntermediateDirectories: true)
let lime = NSColor(srgbRed: 211 / 255, green: 1, blue: 115 / 255, alpha: 1)
let purple = NSColor(srgbRed: 33 / 255, green: 24 / 255, blue: 47 / 255, alpha: 1)
for size in [16, 32, 128, 256, 512] {
    for multiplier in [1, 2] {
        let pixels = size * multiplier
        let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: pixels * 4, bitsPerPixel: 32)!
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
        let context = NSGraphicsContext.current!.cgContext
        context.scaleBy(x: CGFloat(pixels) / 48, y: CGFloat(pixels) / 48)
        context.translateBy(x: 0, y: 48); context.scaleBy(x: 1, y: -1)
        context.setFillColor(lime.cgColor)
        context.addPath(CGPath(roundedRect: CGRect(x: 0, y: 0, width: 48, height: 48), cornerWidth: 14, cornerHeight: 14, transform: nil)); context.fillPath()
        context.setFillColor(purple.cgColor)
        for points in [[CGPoint(x: 24,y: 8),CGPoint(x: 38,y: 16),CGPoint(x: 38,y: 32),CGPoint(x: 24,y: 40),CGPoint(x: 10,y: 32),CGPoint(x: 10,y: 16)], [CGPoint(x: 24,y: 16),CGPoint(x: 17,y: 20),CGPoint(x: 17,y: 28),CGPoint(x: 24,y: 32),CGPoint(x: 31,y: 28),CGPoint(x: 31,y: 20)]] {
            context.move(to: points[0]); for point in points.dropFirst() { context.addLine(to: point) }; context.closePath()
        }
        context.drawPath(using: .eoFill)
        context.setStrokeColor(lime.cgColor);context.setLineWidth(2)
        context.move(to: CGPoint(x: 17,y: 20)); context.addLine(to: CGPoint(x: 24,y: 24)); context.addLine(to: CGPoint(x: 31,y: 20)); context.move(to: CGPoint(x: 24,y: 24)); context.addLine(to: CGPoint(x: 24,y: 32));context.strokePath()
        NSGraphicsContext.restoreGraphicsState()
        let name = "icon_\(size)x\(size)\(multiplier == 2 ? "@2x" : "").png"
        try bitmap.representation(using: .png, properties: [:])!.write(to: output.appendingPathComponent(name))
    }
}
