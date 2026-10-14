import SwiftUI

/// The whole label participates in hit testing, including its empty padding.
public struct FullCardButtonStyle: ButtonStyle {
    public init() { }
    public func makeBody(configuration: ButtonStyleConfiguration) -> some View {
        configuration.label.contentShape(Rectangle()).opacity(configuration.isPressed ? 0.8 : 1)
    }
}
