import Foundation

/// A pure, validated network plan. Constructing this value never changes routes.
public struct PacketTunnelPlan: Codable, Sendable, Equatable {
    public let peerID: String
    public let invitation: String
    public let mtu: Int
    public let ipv4Address: String
    public let ipv6Address: String
    public let dnsServers: [String]
    public let bypassIPv4: [String]
    public let bypassIPv6: [String]

    public init(peerID: String, invitation: String, mtu: Int = 1280) throws {
        guard !peerID.isEmpty, !peerID.contains(where: { $0.isWhitespace }), (576...9000).contains(mtu) else {
            throw ClientError.invalidConfiguration("Choose a valid peer and packet size before connecting a device VPN.")
        }
        let components = invitation.split(separator: "/").map(String.init)
        guard components.count >= 4, components.last == peerID, components.contains("p2p") else {
            throw ClientError.invalidConfiguration("The server invitation must identify the selected peer.")
        }
        // DNS-based invitations are resolved by the peer core before the interface
        // is installed. Provider-owned transport bypasses its own packet tunnel.
        let v4 = components.indices.compactMap { i -> String? in
            guard components[i] == "ip4", i + 1 < components.count else { return nil }
            return components[i + 1]
        }
        let v6 = components.indices.compactMap { i -> String? in
            guard components[i] == "ip6", i + 1 < components.count else { return nil }
            return components[i + 1]
        }
        self.peerID = peerID; self.invitation = invitation; self.mtu = mtu
        ipv4Address = "10.77.0.2"; ipv6Address = "fd77::2"
        dnsServers = ["10.77.0.1", "fd77::1"]
        bypassIPv4 = v4; bypassIPv6 = v6
    }

    public static func packetFamily(_ packet: Data) -> Int32? {
        guard let version = packet.first.map({ $0 >> 4 }) else { return nil }
        if version == 4, packet.count >= 20 { return 2 } // AF_INET on Darwin.
        if version == 6, packet.count >= 40 { return 30 } // AF_INET6 on Darwin.
        return nil
    }
}
