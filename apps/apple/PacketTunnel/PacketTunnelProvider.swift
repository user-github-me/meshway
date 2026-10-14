import Foundation
import NetworkExtension
import Meshway

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let worker = DispatchQueue(label: "network.meshway.packetcore", qos: .userInitiated)
    private let output = DispatchQueue(label: "network.meshway.packetoutput", qos: .userInitiated)
    private let lifecycle = NSLock()
    private var node: MeshwayNode?
    private var stopped = true

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        guard let proto = protocolConfiguration as? NETunnelProviderProtocol,
              let values = proto.providerConfiguration,
              let peerID = values["peer_id"] as? String,
              let invitation = values["invitation"] as? String,
              let shared = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: "group.network.meshway") else {
            completionHandler(NSError(domain: "MeshwayVPN", code: 2, userInfo: [NSLocalizedDescriptionKey: "Missing VPN server or shared-container entitlement."]))
            return
        }
        worker.async { [weak self] in
            guard let self else { return }
            do {
                let plan = try PacketTunnelPlan(peerID: peerID, invitation: invitation)
                let config = try String(contentsOf: shared.appendingPathComponent("active-vpn.json"), encoding: .utf8)
                var startError: NSError?
                guard let node = MeshwayStartSecure(config, try NativeStateKey.loadOrCreate(), &startError) else {
                    throw startError ?? NSError(domain: "MeshwayVPN", code: 3)
                }
                self.lifecycle.lock(); self.node = node; self.stopped = false; self.lifecycle.unlock()
                try node.connect(invitation)
                try node.startPacketVPN(peerID)
                let settings = self.networkSettings(plan: plan)
                self.setTunnelNetworkSettings(settings) { [weak self] error in
                    guard let self else { return }
                    if let error { self.closeCore(); completionHandler(error); return }
                    self.reasserting = false
                    self.readOutgoingPackets()
                    self.output.async { [weak self] in self?.readIncomingPackets() }
                    completionHandler(nil)
                }
            } catch { self.closeCore(); completionHandler(error) }
        }
    }

    private func networkSettings(plan: PacketTunnelPlan) -> NEPacketTunnelNetworkSettings {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "10.77.0.1")
        settings.mtu = NSNumber(value: plan.mtu)
        let ipv4 = NEIPv4Settings(addresses: [plan.ipv4Address], subnetMasks: ["255.255.255.255"])
        ipv4.includedRoutes = [NEIPv4Route.default()]
        ipv4.excludedRoutes = plan.bypassIPv4.map { NEIPv4Route(destinationAddress: $0, subnetMask: "255.255.255.255") }
        settings.ipv4Settings = ipv4
        let ipv6 = NEIPv6Settings(addresses: [plan.ipv6Address], networkPrefixLengths: [128])
        ipv6.includedRoutes = [NEIPv6Route.default()]
        ipv6.excludedRoutes = plan.bypassIPv6.map { NEIPv6Route(destinationAddress: $0, networkPrefixLength: 128) }
        settings.ipv6Settings = ipv6
        let dns = NEDNSSettings(servers: plan.dnsServers)
        dns.matchDomains = [""]; dns.matchDomainsNoSearch = true
        settings.dnsSettings = dns
        return settings
    }

    private var activeNode: MeshwayNode? {
        lifecycle.lock(); defer { lifecycle.unlock() }
        return stopped ? nil : node
    }

    private func readOutgoingPackets() {
        guard activeNode != nil else { return }
        packetFlow.readPackets { [weak self] packets, _ in
            guard let self else { return }
            self.worker.async {
                guard let node = self.activeNode else { return }
                do {
                    for packet in packets where PacketTunnelPlan.packetFamily(packet) != nil { try node.writePacket(packet) }
                } catch { self.reasserting = true }
                self.readOutgoingPackets()
            }
        }
    }

    private func readIncomingPackets() {
        while let node = activeNode {
            do {
                let packet = try node.readPacket()
                guard let family = PacketTunnelPlan.packetFamily(packet) else { continue }
                _ = packetFlow.writePackets([packet], withProtocols: [NSNumber(value: family)])
                reasserting = false
            } catch {
                if activeNode == nil { return }
                // Keep installed default routes and tunneled DNS while the Go
                // core reconnects. Explicit Stop is the only automatic release.
                reasserting = true
                Thread.sleep(forTimeInterval: 0.25)
            }
        }
    }

    private func closeCore() {
        lifecycle.lock(); stopped = true; let previous = node; node = nil; lifecycle.unlock()
        try? previous?.stopPacketVPN()
        try? previous?.stop()
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        closeCore()
        completionHandler()
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        guard String(data: messageData, encoding: .utf8) == "status", let node = activeNode else { completionHandler?(nil); return }
        var error: NSError?
        let status = node.status(&error)
        completionHandler?(error == nil ? status.data(using: .utf8) : nil)
    }
}
