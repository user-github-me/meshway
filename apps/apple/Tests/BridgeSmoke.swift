import Foundation
import Meshway
import Darwin

func startNode(state: URL, exit: Bool, allowed: [String]) throws -> MeshwayNode {
    var error: NSError?
    let raw = MeshwayDefaultConfig(state.path, &error)
    if let error { throw error }
    var object = try JSONSerialization.jsonObject(with: Data(raw.utf8)) as! [String: Any]
    object["listen_addresses"] = ["/ip4/127.0.0.1/tcp/0"]
    object["api_listen"] = ""; object["socks_listen"] = ""
    object["exit"] = exit; object["allow_private_targets"] = exit
    object["allowed_peers"] = allowed
    // A random ephemeral testing key; no native secure-store reads are used.
    let key = Data((0..<32).map { _ in UInt8.random(in: 0...255) })
    guard let node = MeshwayStartSecure(String(decoding: try JSONSerialization.data(withJSONObject: object), as: UTF8.self), key, &error) else { throw error ?? NSError(domain: "smoke", code: 1) }
    return node
}
func status(_ node: MeshwayNode) throws -> [String: Any] {
    var error: NSError?
    let text = node.status(&error)
    if let error { throw error }
    return try JSONSerialization.jsonObject(with: Data(text.utf8)) as! [String: Any]
}
func checksum(_ data: Data) -> UInt16 {
    var sum: UInt32 = 0
    for i in stride(from: 0, to: data.count, by: 2) {
        sum += UInt32(data[i]) << 8
        if i + 1 < data.count { sum += UInt32(data[i + 1]) }
    }
    while sum >> 16 != 0 { sum = (sum & 65535) + (sum >> 16) }
    return ~UInt16(sum)
}
let directory = FileManager.default.temporaryDirectory.appendingPathComponent("meshway-swift-bridge-" + UUID().uuidString)
try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
defer { try? FileManager.default.removeItem(at: directory) }
let dnsSocket = socket(AF_INET, SOCK_DGRAM, 0)
guard dnsSocket >= 0 else { throw NSError(domain: "smoke", code: 4) }
defer { close(dnsSocket) }
var local = sockaddr_in(); local.sin_len = UInt8(MemoryLayout<sockaddr_in>.size); local.sin_family = sa_family_t(AF_INET); local.sin_addr.s_addr = inet_addr("127.0.0.1")
let bound = withUnsafePointer(to: &local) { pointer in pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(dnsSocket, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) } }
guard bound == 0 else { throw NSError(domain: "smoke", code: 5) }
var localLength = socklen_t(MemoryLayout<sockaddr_in>.size)
_ = withUnsafeMutablePointer(to: &local) { pointer in pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(dnsSocket, $0, &localLength) } }
let dnsPort = UInt16(bigEndian: local.sin_port)
DispatchQueue.global().async {
    var bytes = [UInt8](repeating: 0, count: 1500); var source = sockaddr_storage(); var length = socklen_t(MemoryLayout<sockaddr_storage>.size)
    let count = withUnsafeMutablePointer(to: &source) { pointer in pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { recvfrom(dnsSocket, &bytes, bytes.count, 0, $0, &length) } }
    if count >= 12 {
        bytes[2] |= 0x80
        _ = withUnsafePointer(to: &source) { pointer in pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { sendto(dnsSocket, bytes, count, 0, $0, length) } }
    }
}
let client = try startNode(state: directory.appendingPathComponent("client"), exit: false, allowed: [])
defer { try? client.stopPacketVPN(); try? client.stop() }
let clientID = try status(client)["peer_id"] as! String
let server = try startNode(state: directory.appendingPathComponent("server"), exit: true, allowed: [clientID])
defer { try? server.stop() }
let serverState = try status(server)
let serverID = serverState["peer_id"] as! String
let address = (serverState["addresses"] as! [String])[0]
try client.connect(address)
try client.startPacketVPN(serverID)
// Actual IPv4 UDP echo exchange with an ephemeral loopback origin.
// No external network request or OS route/DNS mutation is made.
var packet = Data([0x45,0,0,40,0,1,0,0,64,17,0,0,10,77,0,2,127,0,0,1,0x9c,0x40,UInt8(dnsPort >> 8),UInt8(dnsPort & 255),0,20,0,0,0x42,0x17,1,0,0,0,0,0,0,0,0,0])
let check = checksum(packet.prefix(20)); packet[10] = UInt8(check >> 8); packet[11] = UInt8(check & 255)
try client.writePacket(packet)
let semaphore = DispatchSemaphore(value: 0)
var failure: Error?
DispatchQueue.global().async {
    do {
        let returned = try client.readPacket()
        guard returned.count >= 40, returned[0] >> 4 == 4, returned[16...19] == Data([10,77,0,2]) else { throw NSError(domain: "smoke", code: 2) }
    } catch { failure = error }
    semaphore.signal()
}
guard semaphore.wait(timeout: .now() + 10) == .success else { throw NSError(domain: "smoke", code: 3, userInfo: [NSLocalizedDescriptionKey:"Packet bridge returned no UDP response."]) }
if let failure { throw failure }
print("Actual Swift→Go secure node→encrypted IPv4 UDP packet→Swift packet return passed; no host VPN/routes/DNS/proxy changes.")
