import Foundation
import XCTest
@testable import MeshwayClientCore

final class PacketTunnelPlanTests: XCTestCase {
    func testBothFamiliesAndTunneledDNSArePlanned() throws {
        let plan = try PacketTunnelPlan(peerID: "peer", invitation: "/ip4/203.0.113.5/tcp/4001/p2p/peer")
        XCTAssertEqual(plan.ipv4Address, "10.77.0.2")
        XCTAssertEqual(plan.ipv6Address, "fd77::2")
        XCTAssertEqual(plan.dnsServers, ["10.77.0.1", "fd77::1"])
        XCTAssertEqual(plan.bypassIPv4, ["203.0.113.5"])
        XCTAssertEqual(plan.mtu, 1280)
    }
    func testRelayUnderlayIsExcludedWhilePeerMustMatch() throws {
        let plan = try PacketTunnelPlan(peerID: "exit", invitation: "/ip6/2001:db8::5/tcp/4001/p2p/relay/p2p-circuit/p2p/exit")
        XCTAssertEqual(plan.bypassIPv6, ["2001:db8::5"])
        XCTAssertThrowsError(try PacketTunnelPlan(peerID: "wrong", invitation: plan.invitation))
        XCTAssertThrowsError(try PacketTunnelPlan(peerID: "exit", invitation: ""))
    }
    func testMalformedPacketsNeverReachPacketFlow() {
        XCTAssertNil(PacketTunnelPlan.packetFamily(Data()))
        XCTAssertNil(PacketTunnelPlan.packetFamily(Data([0x45])))
        XCTAssertNil(PacketTunnelPlan.packetFamily(Data(repeating: 0, count: 80)))
        var v4 = Data(repeating: 0, count: 20); v4[0] = 0x45
        var v6 = Data(repeating: 0, count: 40); v6[0] = 0x60
        XCTAssertEqual(PacketTunnelPlan.packetFamily(v4), 2)
        XCTAssertEqual(PacketTunnelPlan.packetFamily(v6), 30)
    }
}
