import Foundation
import Combine
import Darwin
import CryptoKit

public struct NativeMacVPNPlan: Sendable {
    public let bundledExecutable: URL
    public let configuration: URL
    public let peerID: String
    public let underlayIPs: [String]
    public let parentPID: Int32
    public let captureInstance: String

    public init(bundledExecutable: URL, configuration: URL, peerID: String, underlayIPs: [String], parentPID: Int32, captureInstance: String = UUID().uuidString.replacingOccurrences(of:"-",with:"").lowercased()) throws {
        guard bundledExecutable.isFileURL, configuration.isFileURL,
              bundledExecutable.path == bundledExecutable.standardizedFileURL.path,
              configuration.path == configuration.standardizedFileURL.path,
              !bundledExecutable.path.contains("\0"), !configuration.path.contains("\0"),
              peerID.count >= 20, peerID.count <= 128, peerID.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber) }), parentPID > 1 else { throw ClientError.invalidConfiguration("Use the bundled Meshway helper, a private absolute configuration, and an approved server.") }
        let ips = Array(Set(underlayIPs)).sorted()
        guard !ips.isEmpty, ips.count <= 32, ips.allSatisfy(Self.isLiteralIP), captureInstance.count == 32, captureInstance.allSatisfy({ $0.isASCII && ($0.isNumber || ("a"..."f").contains(String($0))) }) else { throw ClientError.invalidConfiguration("Resolve all physical transport addresses before requesting device VPN.") }
        self.bundledExecutable = bundledExecutable; self.configuration = configuration; self.peerID = peerID; self.underlayIPs = ips; self.parentPID = parentPID
        self.captureInstance = captureInstance
    }
    public static func isLiteralIP(_ value: String) -> Bool {
        guard !value.contains("%") else { return false }
        var ipv4 = in_addr(); var ipv6 = in6_addr()
        return value.withCString { inet_pton(AF_INET,$0,&ipv4) == 1 || inet_pton(AF_INET6,$0,&ipv6) == 1 }
    }
    static func shellQuote(_ value: String) -> String { "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'" }
    static func appleScriptQuote(_ value: String) -> String { "\"" + value.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"") + "\"" }
    public func command(executable: URL, action: String) throws -> String {
        guard ["start","stop","status"].contains(action), executable.path.hasPrefix("/private/var/db/meshway-native/") else { throw ClientError.invalidConfiguration("Device VPN can launch only its verified root-owned helper.") }
        var args = [executable.path,"vpn",action,"--config",configuration.path,"--capture-instance",captureInstance]
        if action == "start" { args += ["--peer",peerID,"--underlay-ips",underlayIPs.joined(separator: ","),"--parent-pid",String(parentPID)] }
        return args.map(Self.shellQuote).joined(separator:" ")
    }
    public func installAndStartScript(sha256: String) throws -> (String,URL) {
        guard sha256.count == 64, sha256.allSatisfy({ $0.isASCII && ($0.isNumber || ("a"..."f").contains(String($0))) }) else { throw ClientError.invalidConfiguration("Invalid bundled helper checksum.") }
        let executable = URL(fileURLWithPath:"/private/var/db/meshway-native/meshway-" + sha256)
        let source = Self.shellQuote(bundledExecutable.path), destination = Self.shellQuote(executable.path)
        let script = """
        set -eu
        base='/private/var/db/meshway-native'
        if [ ! -e "$base" ]; then /bin/mkdir -m 755 "$base"; /usr/sbin/chown 0:0 "$base"; fi
        [ ! -L "$base" ] && [ -d "$base" ]
        [ "$(/usr/bin/stat -f '%u:%Lp' "$base")" = '0:755' ]
        target=\(destination)
        if [ ! -e "$target" ]; then
          temporary=$(/usr/bin/mktemp -d "$base/install.XXXXXXXX")
          trap '/bin/rm -rf "$temporary"' EXIT
          /bin/cp \(source) "$temporary/meshway"
          actual=$(/usr/bin/shasum -a 256 "$temporary/meshway")
          [ "${actual%% *}" = '\(sha256)' ]
          /usr/bin/codesign --verify --strict "$temporary/meshway"
          /usr/sbin/chown 0:0 "$temporary/meshway"
          /bin/chmod 755 "$temporary/meshway"
          /bin/mv "$temporary/meshway" "$target"
        fi
        [ ! -L "$target" ] && [ -f "$target" ]
        [ "$(/usr/bin/stat -f '%u:%Lp' "$target")" = '0:755' ]
        actual=$(/usr/bin/shasum -a 256 "$target")
        [ "${actual%% *}" = '\(sha256)' ]
        exec \(try command(executable:executable,action:"start"))
        """
        return ("do shell script " + Self.appleScriptQuote(script) + " with administrator privileges",executable)
    }
    /// Refuse opaque SSH shell hops; the helper cannot exclude endpoints that
    /// an arbitrary ProxyCommand introduces after full-device capture starts.
    public static func sshHost(from output: String) throws -> String {
        var values: [String:String] = [:]
        for line in output.split(whereSeparator: \.isNewline) {
            let fields = line.split(separator:" ",maxSplits:1,omittingEmptySubsequences:true)
            if fields.count == 2 { values[String(fields[0]).lowercased()] = String(fields[1]).trimmingCharacters(in:.whitespaces) }
        }
        guard values["proxycommand"].map({ $0 == "none" }) ?? true,
              values["proxyjump"].map({ $0 == "none" }) ?? true,
              let host = values["hostname"], !host.isEmpty, !host.contains("\n"), !host.contains("\0"), !host.hasPrefix("-") else { throw ClientError.invalidConfiguration("Device VPN needs a direct SSH host. ProxyJump and ProxyCommand require explicit underlay support first.") }
        return host
    }
}

/// Source-only until an exact hosted-Mac qualification marker is shipped. The
/// live personal app never activates this controller during development tests.
@MainActor
public final class NativeMacVPNController: ObservableObject {
    public enum State: String { case disconnected, authorizing, connecting, connected, reconnecting, restoring, recoveryRequired }
    @Published public private(set) var state: State = .disconnected
    @Published public private(set) var error: String?
    @Published public private(set) var device: String?
    public let available: Bool
    public var active: Bool { state != .disconnected && state != .recoveryRequired }
    private var launcher: Process?
    private var installedExecutable: URL?
    private var plan: NativeMacVPNPlan?
    private var monitor: Task<Void,Never>?
    private var configuration: Configuration?
    struct CaptureObservation: Sendable { let active: Bool; let ready: Bool; let peerID: String?; let instance: String?; let device: String? }
    struct OwnedLauncher {
        let isRunning: () -> Bool
        let end: () async -> Void
        let detail: () -> String
        init(isRunning: @escaping () -> Bool,end: @escaping () async -> Void,detail: @escaping () -> String = {""}) { self.isRunning = isRunning;self.end = end;self.detail = detail }
    }
    struct LifecycleHooks {
        let launch: (String) throws -> OwnedLauncher
        let observe: () async throws -> CaptureObservation
        let cleanup: (NativeMacVPNPlan,URL) async throws -> String
        let pause: () async -> Void
        let now: () -> TimeInterval
        let timeout: TimeInterval
    }
    private var lifecycle: LifecycleHooks?
    private var ownedLauncher: OwnedLauncher?

    public init(bundle: Bundle = .main) {
        // This marker is intentionally absent until the root agent records an
        // actual isolated native route, DNS and rollback pass for this build.
        available = bundle.object(forInfoDictionaryKey:"MeshwayAdministratorVPNQualified") as? Bool == true
    }
    public func start(configuration: Configuration, configURL: URL, peerID: String, peerAddresses: [String]) async throws {
        guard available else { throw ClientError.invalidConfiguration("Administrator device VPN is awaiting native route and DNS qualification. SOCKS and personal tunnels remain available.") }
        guard state == .disconnected, let core = Bundle.main.url(forResource:"meshway",withExtension:nil) else { throw ClientError.invalidConfiguration("Stop the previous device VPN and install the complete Meshway app first.") }
        state = .authorizing; error = nil
        do {
            let ips = try await Task.detached {
                let hosts: [String]
                if configuration.personalTransportEnabled {
                    let output = try ProxyWorkerClient.readCommand(executable:URL(fileURLWithPath:"/usr/bin/ssh"),arguments:["-G",configuration.sshAlias])
                    hosts = [try NativeMacVPNPlan.sshHost(from:output)]
                } else {
                    var names = Set<String>()
                    for address in peerAddresses + configuration.bootstrapPeers {
                        let components = address.split(separator:"/").map(String.init)
                        for index in components.indices where ["ip4","ip6","dns","dns4","dns6"].contains(components[index]) && index+1 < components.count { names.insert(components[index+1]) }
                    }
                    hosts = Array(names)
                }
                var addresses = Set<String>()
                for host in hosts { for ip in try Self.resolve(host) { addresses.insert(ip) } }
                return Array(addresses)
            }.value
            let candidate = try NativeMacVPNPlan(bundledExecutable:core,configuration:configURL,peerID:peerID,underlayIPs:ips,parentPID:getpid())
            // Root-install launch is supplied only once the helper's exact
            // readiness/ownership contract has passed the hosted lab.
            self.configuration = configuration
            try await launchVerifiedHelper(candidate)
        } catch { if launcher == nil { state = .disconnected }; self.error = error.localizedDescription; throw error }
    }
    private func launchVerifiedHelper(_ plan: NativeMacVPNPlan) async throws {
        let digest = try await Task.detached { SHA256.hash(data:try Data(contentsOf:plan.bundledExecutable)).map { String(format:"%02x",$0) }.joined() }.value
        let (script, installed) = try plan.installAndStartScript(sha256:digest)
        let (child,diagnostics) = try Self.appleScript(script)
        guard let configuration else { throw ClientError.invalidConfiguration("Device VPN configuration is unavailable.") }
        let api = try DaemonAPI(configuration:configuration)
        launcher = child
        let hooks = LifecycleHooks(launch:{ _ in OwnedLauncher(isRunning:{child.isRunning},end:{await Self.endOwnedLauncher(child)},detail:{diagnostics.text}) },observe:{
            let value = try await api.status().vpn
            return CaptureObservation(active:value?.active == true,ready:value?.capture_ready == true,peerID:value?.peer_id,instance:value?.capture_instance,device:value?.capture_device)
        },cleanup:Self.cleanupVerified,pause:{try? await Task.sleep(for:.milliseconds(250))},now:{ProcessInfo.processInfo.systemUptime},timeout:120)
        try await beginLifecycle(plan:plan,installed:installed,script:script,hooks:hooks)
    }
    /// Production and injected checks use the same ownership/readiness loop.
    func beginLifecycle(plan: NativeMacVPNPlan, installed: URL, script: String, hooks: LifecycleHooks) async throws {
        self.plan = plan; installedExecutable = installed; lifecycle = hooks; state = .connecting
        let child = try hooks.launch(script); ownedLauncher = child
        let deadline = hooks.now() + hooks.timeout
        while hooks.now() < deadline {
            guard child.isRunning() else { state = .recoveryRequired; throw ClientError.invalidConfiguration("The administrator helper ended before capture was confirmed. Review macOS authorization and restore owned networking before retrying. " + child.detail()) }
            let status = try? await hooks.observe()
            if status?.ready == true, status?.peerID == plan.peerID, status?.instance == plan.captureInstance {
                device = status?.device; state = status?.active == true ? .connected : .reconnecting
                monitor = Task { [weak self] in
                    while !Task.isCancelled {
                        await hooks.pause()
                        guard let self else { return }
                        guard child.isRunning() else { self.state = .recoveryRequired; self.error = "The device VPN helper ended. Explicitly restore its owned routes and resolver before reconnecting."; return }
                        let status = try? await hooks.observe()
                        self.state = status?.active == true && status?.ready == true && status?.instance == plan.captureInstance ? .connected : .reconnecting
                    }
                }
                return
            }
            await hooks.pause()
        }
        await child.end()
        // Do not kill shared authorization services. Scoped cleanup below
        // decides whether any late-started root helper owns this journal.
        state = .recoveryRequired
        do { try await stop() }
        catch { throw ClientError.invalidConfiguration("Administrator VPN capture was not confirmed. Owned cleanup still needs attention; keep the peer node running and retry restoration. " + error.localizedDescription) }
        throw ClientError.invalidConfiguration("Administrator VPN capture was not confirmed within two minutes. The owned helper was stopped and its networking restored.")
    }
    public func stop() async throws {
        guard let plan, let installedExecutable else {
            guard state == .disconnected else { throw ClientError.invalidConfiguration("Use explicit device VPN cleanup for this configuration before stopping the daemon.") }
            return
        }
        state = .restoring; monitor?.cancel(); monitor = nil
        do {
            guard let lifecycle else { throw ClientError.invalidConfiguration("The device VPN lifecycle owner is unavailable. Keep the daemon running and restore this configuration explicitly.") }
            let verified = try await lifecycle.cleanup(plan,installedExecutable)
            struct Cleanup: Decodable { let active: Bool; let running: Bool?; let network_cleanup_pending: Bool? }
            let cleanup = try JSONDecoder().decode(Cleanup.self,from:Data(verified.utf8))
            guard !cleanup.active, cleanup.running != true, cleanup.network_cleanup_pending != true else { throw ClientError.invalidConfiguration("Owned device VPN cleanup remains pending. Keep the daemon running and retry restoration.") }
            if let ownedLauncher { await ownedLauncher.end() }
            self.launcher = nil; ownedLauncher = nil; state = .disconnected; device = nil; error = nil
        } catch { state = .recoveryRequired; self.error = error.localizedDescription; throw error }
    }
    nonisolated private static func cleanupVerified(_ plan: NativeMacVPNPlan,_ installedExecutable: URL) async throws -> String {
            let shell = try plan.command(executable:installedExecutable,action:"stop")
            let statusCommand = try plan.command(executable:installedExecutable,action:"status")
            // Both operations share one administrator authorization. Poll the
            // protected journal, rather than treating Stop's marker as cleanup.
            let combined = """
            set -eu
            \(shell)
            attempt=0
            while [ "$attempt" -lt 30 ]; do
              status=$(\(statusCommand))
              if [ "$status" = '{"active":false}' ]; then /usr/bin/printf '%s' "$status"; exit 0; fi
              attempt=$((attempt + 1))
              /bin/sleep 1
            done
            /usr/bin/printf '%s' "$status"
            """
            return try await Task.detached { try Self.runAdministratorScript("do shell script " + NativeMacVPNPlan.appleScriptQuote(combined) + " with administrator privileges",timeout:120) }.value
    }
    nonisolated private static func runAdministratorScript(_ script: String, timeout: TimeInterval) throws -> String {
        // The script contains only verified paths and public transport values;
        // it reaches osascript over stdin, never shell arguments or a file.
        try ProxyWorkerClient.readCommand(executable:URL(fileURLWithPath:"/usr/bin/osascript"),arguments:["-"],timeout:timeout,input:Data(script.utf8))
    }
    private static func appleScript(_ script: String) throws -> (Process,BoundedHelperDiagnostics) {
        let child = Process(); let input = Pipe();let failure = Pipe();let diagnostics=BoundedHelperDiagnostics()
        child.executableURL = URL(fileURLWithPath:"/usr/bin/osascript"); child.arguments = ["-"]
        child.environment = ["PATH":"/usr/bin:/bin:/usr/sbin:/sbin","LC_ALL":"C"]
        child.standardInput = input; child.standardOutput = FileHandle.nullDevice; child.standardError = failure
        failure.fileHandleForReading.readabilityHandler = { handle in
            let data=handle.availableData
            if data.isEmpty { handle.readabilityHandler=nil;try? handle.close() }
            else { diagnostics.append(data) }
        }
        try child.run(); input.fileHandleForReading.closeFile()
        failure.fileHandleForWriting.closeFile()
        try input.fileHandleForWriting.write(contentsOf:Data(script.utf8)); input.fileHandleForWriting.closeFile()
        return (child,diagnostics)
    }
    nonisolated private static func endOwnedLauncher(_ child: Process) async {
        await Task.detached {
            if child.isRunning { child.terminate() }
            let deadline = ProcessInfo.processInfo.systemUptime + 1
            while child.isRunning && ProcessInfo.processInfo.systemUptime < deadline { usleep(10_000) }
            if child.isRunning { _ = Darwin.kill(child.processIdentifier,SIGKILL) }
        }.value
    }
    nonisolated private static func resolve(_ host: String) throws -> [String] {
        if NativeMacVPNPlan.isLiteralIP(host) { return [host] }
        guard let executable = Bundle.main.executableURL else { throw ClientError.invalidConfiguration("Bundled endpoint resolver is unavailable.") }
        let encoded = try JSONEncoder().encode([host])
        let result = try ProxyWorkerClient.readCommand(executable:executable,arguments:["--meshway-resolve-underlay"],timeout:20,input:encoded)
        let ips = try JSONDecoder().decode([String].self,from:Data(result.utf8))
        guard !ips.isEmpty, ips.count <= 32, ips.allSatisfy(NativeMacVPNPlan.isLiteralIP) else { throw ClientError.invalidConfiguration("The owned endpoint resolver returned invalid addresses.") }
        return ips
    }
    public nonisolated static func runResolverIfRequested() -> Bool {
        guard CommandLine.arguments.dropFirst().first == "--meshway-resolve-underlay" else { return false }
        guard CommandLine.arguments.count == 2,
              let data = try? FileHandle.standardInput.readToEnd(), data.count <= 4096,
              let hosts = try? JSONDecoder().decode([String].self,from:data), hosts.count == 1,
              let ips = try? resolveDirect(hosts[0]), let output = try? JSONEncoder().encode(ips) else { return true }
        try? FileHandle.standardOutput.write(contentsOf:output)
        return true
    }
    nonisolated private static func resolveDirect(_ host: String) throws -> [String] {
        var hints = addrinfo(); hints.ai_family = AF_UNSPEC; hints.ai_socktype = SOCK_STREAM; hints.ai_protocol = IPPROTO_TCP
        var result: UnsafeMutablePointer<addrinfo>?
        guard getaddrinfo(host,nil,&hints,&result) == 0, let first = result else { throw ClientError.invalidConfiguration("Could not resolve the physical SSH or peer endpoint.") }
        defer { freeaddrinfo(first) }; var next: UnsafeMutablePointer<addrinfo>? = first; var ips = Set<String>()
        while let current = next {
            var name = [CChar](repeating:0,count:Int(NI_MAXHOST))
            if getnameinfo(current.pointee.ai_addr,current.pointee.ai_addrlen,&name,socklen_t(name.count),nil,0,NI_NUMERICHOST) == 0 { ips.insert(String(cString:name)) }
            next = current.pointee.ai_next
        }
        guard !ips.isEmpty, ips.count <= 32 else { throw ClientError.invalidConfiguration("No bounded physical transport addresses were resolved.") }
        return Array(ips)
    }
}

private final class BoundedHelperDiagnostics: @unchecked Sendable {
    private let lock=NSLock();private var data=Data()
    func append(_ value: Data) { lock.lock();defer {lock.unlock()};data.append(value.prefix(max(0,4096-data.count))) }
    var text: String { lock.lock();defer {lock.unlock()};return String(decoding:data.prefix(512),as:UTF8.self).trimmingCharacters(in:.whitespacesAndNewlines) }
}
