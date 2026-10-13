import Foundation

public struct Peer: Codable, Sendable, Identifiable {
    public let id: String
    public let addresses: [String]?
}

public struct DaemonStatus: Codable, Sendable {
    public struct Roles: Codable, Sendable {
        public let relay: Bool
        public let exit: Bool
    }
    public let version: String
    public let peer_id: String
    public let addresses: [String]
    public let peers: [Peer]
    public let roles: Roles
    public let socks_listen: String
    public let api_listen: String
    public let upstream_peer: String
    public let upstream_connected: Bool?
    public let started_at: String
    public let bytes_sent: UInt64
    public let bytes_received: UInt64
}

public struct Server: Codable, Sendable, Identifiable {
    public var id: String { peer_id }
    public let peer_id: String
    public let label: String
    public let country: String?
    public let address: String
    public let addresses: [String]?
    public let roles: DaemonStatus.Roles
    public let available: Bool
    public let can_connect: Bool
}

public struct DaemonAPI: Sendable {
    private let baseURL: URL
    private let token: String
    private let session: URLSession

    public init(configuration: Configuration, session: URLSession = .shared) throws {
        baseURL = try configuration.apiBaseURL()
        token = configuration.apiToken
        self.session = session
    }

    public func status() async throws -> DaemonStatus {
        try JSONDecoder().decode(DaemonStatus.self, from: await request(path: "v1/status"))
    }

    public func connect(address: String) async throws {
        let body = try JSONSerialization.data(withJSONObject: ["address": address])
        _ = try await request(path: "v1/connect", method: "POST", body: body)
    }

    public func shutdown() async throws {
        _ = try await request(path: "v1/shutdown", method: "POST", body: Data("{}".utf8))
    }

    public func servers() async throws -> [Server] {
        struct Catalog: Decodable { let servers: [Server] }
        return try JSONDecoder().decode(Catalog.self, from: await request(path: "v1/servers")).servers
    }

    public func select(server: Server) async throws {
        let body = try JSONSerialization.data(withJSONObject: ["peer_id": server.peer_id, "address": server.address])
        _ = try await request(path: "v1/select", method: "POST", body: body)
    }

    public func disconnect() async throws {
        _ = try await request(path: "v1/disconnect", method: "POST", body: Data("{}".utf8))
    }

    public func accountSnapshot() async throws -> String {
        async let account = request(path: "v1/account/status")
        async let devices = request(path: "v1/account/devices")
        async let history = request(path: "v1/account/history")
        let values = try await [account, devices, history]
        let labels = ["Account", "Devices", "Local history"]
        return try zip(labels, values).map { label, data in
            let object = try JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed])
            let formatted = try JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys, .fragmentsAllowed])
            return "\(label)\n\(String(decoding: formatted, as: UTF8.self))"
        }.joined(separator: "\n\n")
    }

    public func syncAccount(peerID: String) async throws {
        let body = try JSONSerialization.data(withJSONObject: ["peer_id": peerID])
        _ = try await request(path: "v1/account/sync", method: "POST", body: body)
    }

    private func request(path: String, method: String = "GET", body: Data? = nil) async throws -> Data {
        var request = URLRequest(url: baseURL.appendingPathComponent(path))
        request.httpMethod = method
        request.timeoutInterval = 5
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = body
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw ClientError.invalidConfiguration("Daemon returned a non-HTTP response.")
        }
        guard (200..<300).contains(http.statusCode) else {
            throw ClientError.requestFailed(http.statusCode, String(data: data.prefix(512), encoding: .utf8) ?? "Invalid response")
        }
        return data
    }
}
