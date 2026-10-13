import Foundation
import Security

public enum ClientError: LocalizedError, Equatable {
    case invalidConfiguration(String)
    case requestFailed(Int, String)

    public var errorDescription: String? {
        switch self {
        case .invalidConfiguration(let message): return message
        case .requestFailed(let status, let message): return "Daemon returned HTTP \(status): \(message)"
        }
    }
}

/// Keeps the original JSON dictionary so editing desktop settings preserves
/// forward definitions and new fields added by later daemon versions.
public struct Configuration: Sendable {
    private var rawData: Data
    public var stateDirectory: String
    public var apiListen: String
    public var apiToken: String
    public var socksListen: String
    public var upstreamPeer: String
    public var bootstrapPeers: [String]
    public var allowedPeers: [String]
    public var relay: Bool
    public var exit: Bool
    public var allowPrivateTargets: Bool
    public var personalServer: Bool
    public var advertiseServer: Bool

    public init(data: Data) throws {
        guard let dictionary = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw ClientError.invalidConfiguration("Configuration must be a JSON object.")
        }
        rawData = data
        stateDirectory = dictionary["state_dir"] as? String ?? ""
        apiListen = dictionary["api_listen"] as? String ?? "127.0.0.1:7777"
        apiToken = dictionary["api_token"] as? String ?? ""
        socksListen = dictionary["socks_listen"] as? String ?? "127.0.0.1:1080"
        upstreamPeer = dictionary["upstream_peer"] as? String ?? ""
        bootstrapPeers = dictionary["bootstrap_peers"] as? [String] ?? []
        allowedPeers = dictionary["allowed_peers"] as? [String] ?? []
        relay = dictionary["relay"] as? Bool ?? false
        exit = dictionary["exit"] as? Bool ?? false
        allowPrivateTargets = dictionary["allow_private_targets"] as? Bool ?? false
        personalServer = dictionary["personal_server"] as? Bool ?? false
        advertiseServer = dictionary["advertise"] as? Bool ?? false
        try validate()
    }

    public static func makeDefault(stateDirectory: String) throws -> Configuration {
        var token = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, token.count, &token) == errSecSuccess else {
            throw ClientError.invalidConfiguration("Could not generate a secure local API token.")
        }
        let object: [String: Any] = [
            "state_dir": stateDirectory,
            "listen_addresses": ["/ip4/0.0.0.0/tcp/4001", "/ip4/0.0.0.0/udp/4001/quic-v1"],
            "bootstrap_peers": [], "allowed_peers": [], "upstream_peer": "",
            "socks_listen": "127.0.0.1:1080", "api_listen": "127.0.0.1:7777",
            "api_token": token.map { String(format: "%02x", $0) }.joined(),
            "allowed_origins": [], "relay": false, "exit": false,
            "allow_private_targets": false, "forwards": [],
        ]
        return try Configuration(data: JSONSerialization.data(withJSONObject: object))
    }

    public func validate() throws {
        guard !stateDirectory.isEmpty else { throw ClientError.invalidConfiguration("State directory is required.") }
        guard apiToken.count >= 32, !apiToken.contains(where: { $0.isWhitespace }) else {
            throw ClientError.invalidConfiguration("Local API token must contain at least 32 characters and no whitespace.")
        }
        _ = try apiBaseURL()
        guard socksListen.isEmpty || Self.loopbackURL(socksListen) != nil else {
            throw ClientError.invalidConfiguration("The SOCKS listener must be a loopback host and valid port, or empty to disable it.")
        }
        guard !allowPrivateTargets || exit else {
            throw ClientError.invalidConfiguration("Private destination forwarding requires the internet exit role.")
        }
    }

    public func apiBaseURL() throws -> URL {
        guard let url = Self.loopbackURL(apiListen) else {
            throw ClientError.invalidConfiguration("The local API must listen on 127.0.0.1, localhost, or [::1] with a valid port.")
        }
        return url
    }

    private static func loopbackURL(_ listener: String) -> URL? {
        guard let components = URLComponents(string: "http://\(listener)"),
              ["127.0.0.1", "localhost", "[::1]", "::1"].contains(components.host ?? ""),
              let port = components.port, (1...65535).contains(port),
              components.user == nil, components.password == nil,
              components.path.isEmpty, components.query == nil, components.fragment == nil else { return nil }
        return components.url
    }

    public func encoded() throws -> Data {
        try validate()
        var object = try JSONSerialization.jsonObject(with: rawData) as! [String: Any]
        object["state_dir"] = stateDirectory
        object["api_listen"] = apiListen
        object["api_token"] = apiToken
        object["socks_listen"] = socksListen
        object["upstream_peer"] = upstreamPeer
        object["bootstrap_peers"] = bootstrapPeers
        object["allowed_peers"] = allowedPeers
        object["relay"] = relay
        object["exit"] = exit
        object["allow_private_targets"] = allowPrivateTargets
        object["personal_server"] = personalServer
        object["advertise"] = advertiseServer
        return try JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys])
    }

    public func save(to url: URL) throws {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try encoded().write(to: url, options: [.atomic])
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }
}
