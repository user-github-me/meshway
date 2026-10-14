import Foundation
import Combine
import Security
import SystemConfiguration
import Darwin

/// Explicitly owns the proxy keys it changes. Reads are unprivileged; one
/// authorized preferences transaction changes all enabled network services.
public final class SystemProxyController: ObservableObject, @unchecked Sendable {
    public typealias CommandRunner = ([String], Bool) throws -> String
    public struct ProxyKeys: Codable, Equatable {
        public var host: String?
        public var port: Int?
        public var enabled: Int?
        public var httpEnabled: Int?
        public var httpsEnabled: Int?
        public var pacEnabled: Int?
        public var discoveryEnabled: Int?
        public init(host: String?, port: Int?, enabled: Int?, httpEnabled: Int? = nil, httpsEnabled: Int? = nil, pacEnabled: Int? = nil, discoveryEnabled: Int? = nil) {
            self.host = host; self.port = port; self.enabled = enabled
            self.httpEnabled = httpEnabled; self.httpsEnabled = httpsEnabled; self.pacEnabled = pacEnabled; self.discoveryEnabled = discoveryEnabled
        }
        func meshway(host: String, port: Int) -> ProxyKeys {
            var value = self; value.host = host; value.port = port; value.enabled = 1
            // HTTP/HTTPS and PAC take precedence over SOCKS on macOS. Keep
            // their address/password fields intact; disable only enable flags.
            value.httpEnabled = 0; value.httpsEnabled = 0; value.pacEnabled = 0; value.discoveryEnabled = 0
            return value
        }
    }
    public struct ProxyState: Codable, Equatable {
        public let service: String
        public let enabled: Bool
        public let host: String
        public let port: Int
        public let originalKeys: ProxyKeys?
    }
    public struct Update: Codable { public let service: String; public let keys: ProxyKeys; public let expected: ProxyKeys? }
    private struct Journal: Codable {
        let version: Int
        let owner: UInt32
        let host: String
        let port: Int
        var remaining: [ProxyState]
        var restoring: [String]
        enum CodingKeys: String, CodingKey { case version, owner, host, port, remaining, restoring }
        init(version: Int, owner: UInt32, host: String, port: Int, remaining: [ProxyState], restoring: [String]) {
            self.version = version; self.owner = owner; self.host = host; self.port = port; self.remaining = remaining; self.restoring = restoring
        }
        init(from decoder: Decoder) throws {
            let values = try decoder.container(keyedBy: CodingKeys.self)
            version = try values.decode(Int.self, forKey: .version); owner = try values.decode(UInt32.self, forKey: .owner)
            host = try values.decode(String.self, forKey: .host); port = try values.decode(Int.self, forKey: .port)
            remaining = try values.decode([ProxyState].self, forKey: .remaining)
            restoring = try values.decodeIfPresent([String].self, forKey: .restoring) ?? []
        }
    }
    @Published public private(set) var enabled = false
    @Published public private(set) var needsRecovery = false
    @Published public private(set) var error: String?
    @Published public private(set) var changedExternally = false
    private var currentEnabled = false
    private var currentRecovery = false
    private var currentError: String?
    private var currentChangedExternally = false
    private let operationQueue = DispatchQueue(label: "network.meshway.system-proxy")
    private let path: URL
    private let runner: CommandRunner
    private let snapshotter: ((String) throws -> ProxyKeys)?
    private let effectiveChecker: (String, Int) -> Bool
    private var journal: Journal?
    private var lock: Int32 = -1
    private var ownsLock = false

    public init(directory: URL, runner: CommandRunner? = nil, snapshotter: ((String) throws -> ProxyKeys)? = nil, effectiveChecker: @escaping (String, Int) -> Bool = SystemProxyController.effectiveProxyMatches) {
        path = directory.appendingPathComponent("system-proxy-restore.json")
        self.effectiveChecker = effectiveChecker
        if let runner { self.runner = runner }
        else {
            let authorized = AuthorizedProxyPreferences()
            let worker = ProxyWorkerClient()
            self.runner = { arguments, privileged in
                guard privileged else { return try authorized.run(arguments, privileged: false) }
                guard arguments.count == 2, arguments[0] == "-applymeshwayproxy", let data = Data(base64Encoded: arguments[1]), data.count <= ProxyWorkerClient.maximumFrame else { throw ClientError.invalidConfiguration("Invalid proxy transaction.") }
                try worker.apply(JSONDecoder().decode([Update].self, from: data))
                return ""
            }
        }
        if let snapshotter { self.snapshotter = snapshotter }
        else if runner == nil { self.snapshotter = Self.proxyKeys }
        else { self.snapshotter = nil }
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            lock = open(directory.appendingPathComponent("system-proxy.lock").path, O_CREAT | O_RDWR | O_NOFOLLOW, 0o600)
            guard lock >= 0, flock(lock, LOCK_EX | LOCK_NB) == 0 else { throw failure("Another Meshway app owns system-proxy recovery. Close it first.") }
            ownsLock = true
            if FileManager.default.fileExists(atPath: path.path) {
                journal = try loadJournal(); currentRecovery = true
            }
        } catch { currentError = error.localizedDescription }
        publishState()
    }
    deinit { if lock >= 0 { if ownsLock { _ = flock(lock, LOCK_UN) }; close(lock) } }

    public func enable(host: String, port: Int) throws {
        defer { publishState() }
        guard ownsLock, currentError == nil, journal == nil else { throw failure(currentError ?? "Restore the previous system proxy before enabling it again.") }
        guard ["127.0.0.1", "::1"].contains(host), (1...65535).contains(port) else { throw failure("The system proxy must use the local Meshway SOCKS listener.") }
        let output = try runner(["-listallnetworkservices"], false)
        let services = output.split(whereSeparator: \.isNewline).map(String.init).dropFirst().filter { !$0.hasPrefix("*") && !$0.isEmpty }
        guard !services.isEmpty, services.count <= 128 else { throw failure("No supported network services are available.") }
        let previous = try services.map { try state(for: $0) }
        // Never read unrelated proxy credentials. An authenticated SOCKS proxy
        // cannot be safely overwritten and restored through this flow.
        journal = Journal(version: 2, owner: getuid(), host: host, port: port, remaining: previous, restoring: [])
        do { try persist() } catch { journal = nil; throw error }
        do {
            let updates = previous.map { item in
                Update(service: item.service, keys: (item.originalKeys ?? ProxyKeys(host: item.host, port: item.port, enabled: item.enabled ? 1 : 0)).meshway(host: host, port: port), expected: item.originalKeys)
            }
            try apply(updates)
            for item in previous {
                let current = try state(for: item.service)
                guard owned(current, by: journal!) else { throw failure("macOS did not enable Meshway’s proxy for \(item.service).") }
            }
            currentEnabled = true; currentRecovery = false; currentChangedExternally = false; currentError = nil
        } catch {
            let original = error
            if original is ProxyWorkerFailure {
                // The child may have committed before its reply was lost.
                // Retain the journal for an explicit read/merge restoration;
                // do not start another authorization prompt automatically.
                currentRecovery = true; currentError = original.localizedDescription
                throw original
            }
            do { try restore() } catch { currentError = "\(original.localizedDescription) Restore the previous proxy before reconnecting: \(error.localizedDescription)" }
            throw original
        }
    }

    public func restore() throws {
        defer { publishState() }
        guard ownsLock else { throw failure("Another Meshway app owns system-proxy recovery. Close it first.") }
        guard var saved = journal else { currentEnabled = false; currentRecovery = false; return }
        var updates: [Update] = []
        for previous in saved.remaining {
            let current = try state(for: previous.service)
            if current == previous { continue }
            let target: ProxyKeys
            if let original = previous.originalKeys, let now = current.originalKeys {
                var restored = now
                let expected = original.meshway(host: saved.host, port: saved.port)
                let partial = saved.restoring.contains(previous.service)
                // Restore only fields that still match our change. A later
                // HTTP/PAC edit does not abandon restoration of our SOCKS keys.
                if now.host == expected.host || (partial && now.host == original.host) { restored.host = original.host }
                if now.port == expected.port || (partial && now.port == original.port) { restored.port = original.port }
                let ownsSOCKS = (now.host == expected.host && now.port == expected.port) || (partial && now.host == original.host && now.port == original.port)
                if ownsSOCKS && (now.enabled == expected.enabled || partial) { restored.enabled = original.enabled }
                if now.enabled == 0 { restored.enabled = 0 }
                if saved.version == 2 && now.enabled == 1 && now.host == expected.host && now.port == expected.port {
                    if now.httpEnabled == expected.httpEnabled { restored.httpEnabled = original.httpEnabled }
                    if now.httpsEnabled == expected.httpsEnabled { restored.httpsEnabled = original.httpsEnabled }
                    if now.pacEnabled == expected.pacEnabled { restored.pacEnabled = original.pacEnabled }
                    if now.discoveryEnabled == expected.discoveryEnabled { restored.discoveryEnabled = original.discoveryEnabled }
                }
                target = restored
                if target == now { continue }
            } else {
                let partial = saved.restoring.contains(previous.service) && current.host == previous.host && current.port == previous.port
                guard current.host == saved.host && current.port == saved.port || partial else { continue }
                target = ProxyKeys(host: previous.host, port: previous.port, enabled: previous.enabled ? 1 : 0)
            }
            if !saved.restoring.contains(previous.service) { saved.restoring.append(previous.service) }
            updates.append(Update(service: previous.service, keys: target, expected: current.originalKeys))
        }
        journal = saved; try persist()
        do {
            if !updates.isEmpty { try apply(updates) }
            for update in updates {
                let current = try state(for: update.service)
                if let keys = current.originalKeys { guard keys == update.keys else { throw failure("Could not verify restored proxy keys.") } }
                else { guard current.host == (update.keys.host ?? ""), current.port == (update.keys.port ?? 0), current.enabled == (update.keys.enabled == 1) else { throw failure("Could not verify restored proxy settings.") } }
            }
            try FileManager.default.removeItem(at: path)
            journal = nil; currentEnabled = false; currentRecovery = false; currentChangedExternally = false; currentError = nil
        } catch {
            currentRecovery = true
            currentError = "The previous proxy still needs restoration. Recovery information remains saved. \(error.localizedDescription)"
            throw failure(currentError!)
        }
    }

    public func enableAsync(host: String, port: Int) async throws {
        try await withCheckedThrowingContinuation { continuation in operationQueue.async { do { try self.enable(host: host, port: port); continuation.resume() } catch { continuation.resume(throwing: error) } } }
    }
    public func restoreAsync() async throws {
        try await withCheckedThrowingContinuation { continuation in operationQueue.async { do { try self.restore(); continuation.resume() } catch { continuation.resume(throwing: error) } } }
    }
    /// Resume only this user's fully applied journal and matching live daemon
    /// endpoint. This reads preferences without authorization or any write.
    /// The caller must independently verify internet access after resuming.
    public func resumeOwnAppliedJournalAsync(host: String, port: Int) async -> Bool {
        await withCheckedContinuation { continuation in
            operationQueue.async {
                var resumed = false
                defer { self.publishState(); continuation.resume(returning: resumed) }
                guard self.ownsLock, self.journal != nil, !self.currentEnabled,
                      ["127.0.0.1", "::1"].contains(host), (1...65535).contains(port) else {
                    return
                }
                do {
                    let saved = try self.loadJournal()
                    self.journal = saved
                    guard saved.version == 2, saved.host == host, saved.port == port,
                          saved.restoring.isEmpty, !saved.remaining.isEmpty else {
                        return
                    }
                    for previous in saved.remaining {
                        guard let original = previous.originalKeys else { return }
                        let current = try self.state(for: previous.service)
                        guard current.enabled, self.owned(current, by: saved), current.originalKeys == original.meshway(host: host, port: port) else {
                            throw self.failure("System proxy changed outside Meshway. Previous settings remain saved for explicit restoration.")
                        }
                    }
                    guard self.effectiveChecker(host, port) else {
                        throw self.failure("The active macOS proxy no longer matches this Meshway daemon. Previous settings remain saved for explicit restoration.")
                    }
                    self.currentEnabled = true; self.currentRecovery = false
                    self.currentChangedExternally = false; self.currentError = nil
                    resumed = true
                } catch {
                    self.currentEnabled = false; self.currentRecovery = true
                    self.currentChangedExternally = true; self.currentError = error.localizedDescription
                }
            }
        }
    }
    /// Observe drift; never reapply over another app or a later user change.
    public func verifyStillAppliedAsync() async -> Bool {
        await withCheckedContinuation { continuation in
            operationQueue.async {
                guard self.currentEnabled, let saved = self.journal else { continuation.resume(returning: false); return }
                do {
                    for previous in saved.remaining {
                        let current = try self.state(for: previous.service)
                        guard current.enabled, self.owned(current, by: saved) else { throw self.failure("System proxy changed outside Meshway. Enable Meshway’s proxy again when you want supported apps to use it.") }
                    }
                    guard self.effectiveChecker(saved.host, saved.port) else { throw self.failure("The active macOS proxy no longer points to Meshway. Another app or a network change may have changed it.") }
                    continuation.resume(returning: true)
                } catch {
                    self.currentEnabled = false; self.currentRecovery = true; self.currentChangedExternally = true
                    self.currentError = error.localizedDescription; self.publishState()
                    continuation.resume(returning: false)
                }
            }
        }
    }
    public static func effectiveProxyMatches(_ host: String, _ port: Int) -> Bool {
        guard let values = SCDynamicStoreCopyProxies(nil) as? [String: Any] else { return false }
        func number(_ key: CFString) -> Int { (values[key as String] as? NSNumber)?.intValue ?? 0 }
        return values[kSCPropNetProxiesSOCKSProxy as String] as? String == host && number(kSCPropNetProxiesSOCKSPort) == port && number(kSCPropNetProxiesSOCKSEnable) == 1 && [kSCPropNetProxiesHTTPEnable,kSCPropNetProxiesHTTPSEnable,kSCPropNetProxiesProxyAutoConfigEnable,kSCPropNetProxiesProxyAutoDiscoveryEnable].allSatisfy { number($0) == 0 }
    }

    private func publishState() {
        let enabled = currentEnabled, recovery = currentRecovery, changed = currentChangedExternally, error = currentError
        if Thread.isMainThread { self.enabled = enabled; self.needsRecovery = recovery; self.changedExternally = changed; self.error = error }
        else { DispatchQueue.main.async { self.enabled = enabled; self.needsRecovery = recovery; self.changedExternally = changed; self.error = error } }
    }
    private func owned(_ current: ProxyState, by saved: Journal) -> Bool {
        guard current.host == saved.host, current.port == saved.port else { return false }
        guard let keys = current.originalKeys else { return true }
        return keys.enabled == 1 && keys.httpEnabled == 0 && keys.httpsEnabled == 0 && keys.pacEnabled == 0 && keys.discoveryEnabled == 0
    }
    private func apply(_ updates: [Update]) throws {
        let encoded = try JSONEncoder().encode(updates).base64EncodedString()
        _ = try runner(["-applymeshwayproxy", encoded], true)
    }
    private func state(for service: String) throws -> ProxyState {
        guard !service.isEmpty, !service.contains("\n"), !service.contains("\0") else { throw failure("Invalid network service.") }
        let output = try runner(["-getsocksfirewallproxy", service], false)
        var fields: [String: String] = [:]
        for line in output.split(whereSeparator: \.isNewline) {
            let parts = line.split(separator: ":", maxSplits: 1, omittingEmptySubsequences: false)
            if parts.count == 2 { fields[String(parts[0])] = parts[1].trimmingCharacters(in: .whitespaces) }
        }
        guard let value = fields["Enabled"], ["Yes", "No"].contains(value), let host = fields["Server"],
              let portText = fields["Port"], let port = Int(portText), (0...65535).contains(port),
              let authentication = fields["Authenticated Proxy Enabled"], ["0", "No"].contains(authentication) else {
            throw failure("Existing authenticated SOCKS settings cannot be safely replaced. Your current proxy was preserved.")
        }
        return ProxyState(service: service, enabled: value == "Yes", host: host, port: port, originalKeys: try snapshotter?(service))
    }
    private func persist() throws {
        guard let journal else { return }
        try JSONEncoder().encode(journal).write(to: path, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
    }
    private func loadJournal() throws -> Journal {
        let attrs = try FileManager.default.attributesOfItem(atPath: path.path)
        guard attrs[.type] as? FileAttributeType == .typeRegular,
              (attrs[.ownerAccountID] as? NSNumber)?.uint32Value == getuid(),
              (attrs[.posixPermissions] as? NSNumber)?.intValue == 0o600 else { throw failure("The system-proxy recovery file is not a private file owned by this user.") }
        let data = try Data(contentsOf: path)
        guard data.count <= 128 * 1024 else { throw failure("The system-proxy recovery file is too large.") }
        let saved = try JSONDecoder().decode(Journal.self, from: data)
        let names = saved.remaining.map(\.service)
        guard [1, 2].contains(saved.version), saved.owner == getuid(), names.count <= 128,
              Set(names).count == names.count, names.allSatisfy({ !$0.isEmpty && !$0.contains("\n") && !$0.contains("\0") }),
              ["127.0.0.1", "::1"].contains(saved.host), (1...65535).contains(saved.port) else { throw failure("Invalid system-proxy recovery journal. Keep the file and restore your previous settings manually.") }
        return saved
    }
    private func failure(_ message: String) -> ClientError { .invalidConfiguration(message) }

    public static func proxyKeys(_ name: String) throws -> ProxyKeys {
        guard let preferences = SCPreferencesCreate(nil, "Meshway proxy snapshot" as CFString, nil),
              let services = SCNetworkServiceCopyAll(preferences) as? [SCNetworkService],
              let service = services.first(where: { (SCNetworkServiceGetName($0) as String?) == name }),
              let proxies = SCNetworkServiceCopyProtocol(service, kSCNetworkProtocolTypeProxies) else { throw ClientError.invalidConfiguration("Could not snapshot the original proxy keys.") }
        let values = (SCNetworkProtocolGetConfiguration(proxies) as? [String: Any]) ?? [:]
        func integer(_ key: CFString) -> Int? { (values[key as String] as? NSNumber)?.intValue }
        return ProxyKeys(host: values[kSCPropNetProxiesSOCKSProxy as String] as? String, port: integer(kSCPropNetProxiesSOCKSPort), enabled: integer(kSCPropNetProxiesSOCKSEnable), httpEnabled: integer(kSCPropNetProxiesHTTPEnable), httpsEnabled: integer(kSCPropNetProxiesHTTPSEnable), pacEnabled: integer(kSCPropNetProxiesProxyAutoConfigEnable), discoveryEnabled: integer(kSCPropNetProxiesProxyAutoDiscoveryEnable))
    }
}

final class AuthorizedProxyPreferences: @unchecked Sendable {
    private var authorization: AuthorizationRef?
    deinit { if let authorization { AuthorizationFree(authorization, []) } }
    func run(_ arguments: [String], privileged: Bool) throws -> String {
        guard privileged else {
            guard ["-listallnetworkservices", "-getsocksfirewallproxy"].contains(arguments.first ?? ""), arguments.allSatisfy({ !$0.contains("\0") && !$0.contains("\n") }) else { throw ClientError.invalidConfiguration("Invalid proxy read command.") }
            return try ProxyWorkerClient.readCommand(executable: URL(fileURLWithPath: "/usr/sbin/networksetup"), arguments: arguments)
        }
        guard arguments.count == 2, arguments[0] == "-applymeshwayproxy", let data = Data(base64Encoded: arguments[1]), data.count <= 128 * 1024 else { throw ClientError.invalidConfiguration("Invalid proxy transaction.") }
        try apply(JSONDecoder().decode([SystemProxyController.Update].self, from: data))
        return ""
    }
    func apply(_ updates: [SystemProxyController.Update]) throws {
        guard !updates.isEmpty, updates.count <= 128 else { throw ClientError.invalidConfiguration("Invalid network-service transaction.") }
        if authorization == nil {
            guard AuthorizationCreate(nil, nil, [], &authorization) == errAuthorizationSuccess else { throw ClientError.invalidConfiguration("macOS could not authorize the proxy change.") }
        }
        guard let authorization else { throw ClientError.invalidConfiguration("macOS authorization is unavailable.") }
        let name = strdup("system.preferences.network")!; defer { free(name) }
        var right = AuthorizationItem(name: UnsafePointer(name), valueLength: 0, value: nil, flags: 0)
        let authorized = withUnsafeMutablePointer(to: &right) { pointer in
            var rights = AuthorizationRights(count: 1, items: pointer)
            return AuthorizationCopyRights(authorization, &rights, nil, [.interactionAllowed, .extendRights, .preAuthorize], nil)
        }
        guard authorized == errAuthorizationSuccess else { throw ClientError.invalidConfiguration("Proxy authorization was cancelled or denied. Existing settings were preserved.") }
        guard let preferences = SCPreferencesCreateWithAuthorization(nil, "Meshway system proxy" as CFString, nil, authorization) else { throw ClientError.invalidConfiguration("macOS network preferences could not be authorized.") }
        // False avoids waiting for ordinary lock contention. Authorization and
        // privileged helper IPC may still wait; the owned worker bounds them.
        guard SCPreferencesLock(preferences, false) else { throw ClientError.invalidConfiguration("macOS network preferences are busy. Existing settings and recovery information were preserved; retry shortly.") }
        defer { SCPreferencesUnlock(preferences) }
        guard let services = SCNetworkServiceCopyAll(preferences) as? [SCNetworkService] else { throw ClientError.invalidConfiguration("No network services are available.") }
        for update in updates {
            guard !update.service.isEmpty, !update.service.contains("\0"), !update.service.contains("\n"),
                  let service = services.first(where: { (SCNetworkServiceGetName($0) as String?) == update.service }),
                  let proxies = SCNetworkServiceCopyProtocol(service, kSCNetworkProtocolTypeProxies) else { throw ClientError.invalidConfiguration("A network service is no longer available.") }
            var values = (SCNetworkProtocolGetConfiguration(proxies) as? [String: Any]) ?? [:]
            let keys = update.keys
            if let expected = update.expected {
                func number(_ key: CFString) -> Int? { (values[key as String] as? NSNumber)?.intValue }
                let current = SystemProxyController.ProxyKeys(host: values[kSCPropNetProxiesSOCKSProxy as String] as? String, port: number(kSCPropNetProxiesSOCKSPort), enabled: number(kSCPropNetProxiesSOCKSEnable), httpEnabled: number(kSCPropNetProxiesHTTPEnable), httpsEnabled: number(kSCPropNetProxiesHTTPSEnable), pacEnabled: number(kSCPropNetProxiesProxyAutoConfigEnable), discoveryEnabled: number(kSCPropNetProxiesProxyAutoDiscoveryEnable))
                guard current == expected else { throw ClientError.invalidConfiguration("Proxy settings changed while authorization was open. No Meshway transaction was committed; retry with the current settings.") }
            }
            let owned = [kSCPropNetProxiesSOCKSProxy, kSCPropNetProxiesSOCKSPort, kSCPropNetProxiesSOCKSEnable, kSCPropNetProxiesHTTPEnable, kSCPropNetProxiesHTTPSEnable, kSCPropNetProxiesProxyAutoConfigEnable, kSCPropNetProxiesProxyAutoDiscoveryEnable]
            for key in owned { values.removeValue(forKey: key as String) }
            if let host = keys.host { values[kSCPropNetProxiesSOCKSProxy as String] = host }
            if let port = keys.port { values[kSCPropNetProxiesSOCKSPort as String] = port }
            if let value = keys.enabled { values[kSCPropNetProxiesSOCKSEnable as String] = value }
            if let value = keys.httpEnabled { values[kSCPropNetProxiesHTTPEnable as String] = value }
            if let value = keys.httpsEnabled { values[kSCPropNetProxiesHTTPSEnable as String] = value }
            if let value = keys.pacEnabled { values[kSCPropNetProxiesProxyAutoConfigEnable as String] = value }
            if let value = keys.discoveryEnabled { values[kSCPropNetProxiesProxyAutoDiscoveryEnable as String] = value }
            guard SCNetworkProtocolSetConfiguration(proxies, values as CFDictionary) else { throw ClientError.invalidConfiguration("Could not stage the proxy change. No preferences were committed.") }
        }
        guard SCPreferencesCommitChanges(preferences), SCPreferencesApplyChanges(preferences) else { throw ClientError.invalidConfiguration("macOS did not apply the proxy transaction (\(SCError())). Recovery information remains saved.") }
    }
}
