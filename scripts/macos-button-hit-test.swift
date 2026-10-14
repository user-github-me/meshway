import AppKit
import SwiftUI

final class HitCounter: ObservableObject {
    @Published var main = 0
    @Published var mode = 0
    @Published var sidebar = 0
}
struct RectMarker: NSViewRepresentable {
    let name: String
    func makeNSView(context: Context) -> NSView { let view = NSView(); view.identifier = NSUserInterfaceItemIdentifier(name); return view }
    func updateNSView(_ view: NSView, context: Context) { }
}
struct TestButtons: View {
    @ObservedObject var counter: HitCounter
    var body: some View {
        VStack(spacing: 12) {
            Button { counter.main += 1 } label: {
                HStack { Image(systemName: "bolt.fill"); Text("Connect").fontWeight(.semibold) }.padding(.horizontal, 23).padding(.vertical, 14)
                    .foregroundStyle(.black).background(Color.green, in: RoundedRectangle(cornerRadius: 12)).contentShape(RoundedRectangle(cornerRadius: 12))
            }.buttonStyle(FullCardButtonStyle()).background(RectMarker(name: "main"))
            Button { counter.mode += 1 } label: {
                VStack(alignment: .leading, spacing: 8) { HStack { Image(systemName: "lock.shield"); Text("Personal tunnel"); Spacer() }; Text("Your own server").font(.caption) }.padding(13).frame(width: 220, alignment: .leading)
                    .background(Color.gray, in: RoundedRectangle(cornerRadius: 12)).contentShape(RoundedRectangle(cornerRadius: 12))
            }.buttonStyle(FullCardButtonStyle()).background(RectMarker(name: "mode"))
            Button { counter.sidebar += 1 } label: {
                HStack { Image(systemName: "server.rack"); Text("Servers"); Spacer() }.padding(.horizontal, 13).padding(.vertical, 13).frame(width: 220).background(Color.gray, in: RoundedRectangle(cornerRadius: 11)).contentShape(Rectangle())
            }.buttonStyle(FullCardButtonStyle()).background(RectMarker(name: "sidebar"))
        }.padding(20)
    }
}
@main
struct ButtonHitTest {
 @MainActor static func main() {
let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let counter = HitCounter()
let hosting = NSHostingView(rootView: TestButtons(counter: counter))
let window = NSWindow(contentRect: NSRect(x: 50, y: 50, width: 310, height: 270), styleMask: [.titled], backing: .buffered, defer: false)
window.contentView = hosting
window.makeKeyAndOrderFront(nil)
hosting.layoutSubtreeIfNeeded()
RunLoop.current.run(until: Date().addingTimeInterval(0.1))
func markers(_ view: NSView) -> [NSView] { (view.identifier == nil ? [] : [view]) + view.subviews.flatMap(markers) }
var points: [(String,NSPoint)] = []
for marker in markers(hosting) {
    guard let name = marker.identifier?.rawValue, ["main","mode","sidebar"].contains(name) else { continue }
    let frame = marker.convert(marker.bounds, to: nil)
    print(name,frame)
    points.append((name, NSPoint(x: frame.minX + 7, y: frame.midY)))
    points.append((name, NSPoint(x: frame.maxX - 7, y: frame.midY)))
}
for (label,point) in points {
 print("click",label,point)
    if let event = NSEvent.mouseEvent(with: .leftMouseDown, location: point, modifierFlags: [], timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1) { window.sendEvent(event) }
    if let event = NSEvent.mouseEvent(with: .leftMouseUp, location: point, modifierFlags: [], timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 0) { window.sendEvent(event) }
    RunLoop.current.run(until: Date().addingTimeInterval(0.03))
}
guard counter.main == 2, counter.mode == 2, counter.sidebar == 2 else { fatalError("Rounded button edge hit target failed") }
print("PASS: two edge clicks each on main Connect, mode card, and sidebar row; no Accessibility permission or network changes.")
window.orderOut(nil)

 }
}
