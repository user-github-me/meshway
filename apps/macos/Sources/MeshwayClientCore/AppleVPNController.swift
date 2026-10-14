import Foundation
import NetworkExtension
import Combine

@MainActor
public final class AppleVPNController: ObservableObject {
    public static let appGroup = "group.network.meshway"
    public static var sharedContainer: URL? {
        guard NativeStateKey.hasEntitlement("com.apple.security.application-groups", containing: appGroup) else { return nil }
        return FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup)
    }
    @Published public private(set) var status: NEVPNStatus = .invalid
    @Published public private(set) var error: String?
    private var manager: NETunnelProviderManager?
    private var observer: NSObjectProtocol?

    public init() {
        observer = NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange, object: nil, queue: .main) { [weak self] _ in
            Task { @MainActor [weak self] in self?.status = self?.manager?.connection.status ?? .invalid }
        }
        Task { await refresh() }
    }

    public var statusText: String {
        switch status {
        case .connected: return "Device VPN connected"
        case .connecting: return "Connecting device VPN…"
        case .reasserting: return "Reconnecting · internet remains blocked"
        case .disconnecting: return "Disconnecting device VPN…"
        case .disconnected: return "Device VPN disconnected"
        default: return "Device VPN is not configured"
        }
    }

    public func refresh() async {
        do {
            manager = try await NETunnelProviderManager.loadAllFromPreferences().first(where: {
                ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == Self.providerBundleID
            })
            status = manager?.connection.status ?? .invalid
        } catch { self.error = error.localizedDescription }
    }

    public static var providerBundleID: String {
        #if os(macOS)
        "network.meshway.desktop.PacketTunnel"
        #else
        "network.meshway.ios.PacketTunnel"
        #endif
    }

    public func connect(config: Data, peerID: String, invitation: String) async throws {
        guard let shared = Self.sharedContainer else {
            throw NSError(domain: "MeshwayVPN", code: 1, userInfo: [NSLocalizedDescriptionKey: "Use the signed Meshway application with its packet extension to connect the device VPN."])
        }
        // Configuration is private app-group storage. Credentials are never put
        // in VPN preferences, logs, or the manager's providerConfiguration.
        var object = try JSONSerialization.jsonObject(with: config) as! [String: Any]
        object["state_dir"] = shared.appendingPathComponent("node").path
        object["api_listen"] = ""; object["socks_listen"] = ""
        object["listen_addresses"] = ["/ip4/0.0.0.0/tcp/0", "/ip4/0.0.0.0/udp/0/quic-v1"]
        object["upstream_peer"] = peerID
        object["vpn"] = ["enabled": true, "internet": true, "mtu": 1280]
        let path = shared.appendingPathComponent("active-vpn.json")
        try JSONSerialization.data(withJSONObject: object).write(to: path, options: .atomic)
        #if os(iOS)
        try FileManager.default.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: path.path)
        #else
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
        #endif
        let manager = self.manager ?? NETunnelProviderManager()
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = Self.providerBundleID
        proto.serverAddress = "Meshway encrypted peer"
        proto.providerConfiguration = ["peer_id": peerID, "invitation": invitation]
        // Explicit disconnect releases routes; a lost peer keeps routes/DNS in
        // the running provider and retries. Both address families are included.
        proto.disconnectOnSleep = false
        proto.includeAllNetworks = true
        proto.excludeLocalNetworks = false
        manager.protocolConfiguration = proto
        manager.localizedDescription = "Meshway"
        manager.isEnabled = true
        try await manager.saveToPreferences()
        try await manager.loadFromPreferences()
        self.manager = manager
        try manager.connection.startVPNTunnel()
        status = manager.connection.status
        error = nil
    }

    public func disconnect() { manager?.connection.stopVPNTunnel(); status = manager?.connection.status ?? .invalid }
}
