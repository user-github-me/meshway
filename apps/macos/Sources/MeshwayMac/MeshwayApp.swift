import AppKit
import SwiftUI
import Darwin
#if SWIFT_PACKAGE
import MeshwayClientCore
#endif

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    weak var model: AppModel?
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let model, model.systemProxy.enabled || model.systemProxy.needsRecovery || model.activeVPN || model.nativeVPN.state == .recoveryRequired || model.busy else { return .terminateNow }
        Task { sender.reply(toApplicationShouldTerminate: await model.restoreForQuit()) }
        return .terminateLater
    }
    func applicationWillTerminate(_ notification: Notification) {
        model?.terminateOwnedProcess()
    }
}

@main
struct MeshwayEntryPoint {
    @MainActor static func main() {
        if ProxyWorkerServer.runIfRequested() || NativeMacVPNController.runResolverIfRequested() { return }
        MeshwayApplication.main()
    }
}

struct MeshwayApplication: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var model = AppModel()

    var body: some Scene {
        WindowGroup("Meshway") {
            ContentView(model: model)
                .onAppear { delegate.model = model; model.startSOCKSIfRequested() }
                .frame(minWidth: 980, minHeight: 720)
                .preferredColorScheme(.dark)
        }
        .windowStyle(.titleBar)
        .commands {
            CommandGroup(replacing: .newItem) { }
        }
    }
}

@MainActor
final class AppModel: ObservableObject {
    @Published var configuration: Configuration
    @Published var status: DaemonStatus?
    @Published var message = "Ready. Add a peer or bootstrap address to join a network."
    @Published var error: String?
    @Published var busy = false
    @Published var technicalError: String?
    @Published var storageAccessNeeded = false
    private var allowStorageInteraction = false
    @Published var internetVerified = false
    @Published var observedExitIP: String?
    @Published var runningOwnedProcess = false
    @Published var executablePath: String
    @Published var connectAddress = ""
    @Published var bootstrapText = ""
    @Published var allowedPeersText = ""
    @Published var accountText = "Start the daemon, then refresh your local account and device list."
    @Published var accountSyncPeer = ""
    @Published var revokedPeer = ""
    @Published var syncHistoryDeletion = true
    @Published var servers: [Server] = []
    @Published var catalogMessage = "Start Meshway to see available servers."
    @Published var requestedPeer: String = ""
    @Published var explicitDisconnected = false
    @Published var connectionMode: ConnectionMode = .socks
    @Published var sentPerSecond: Double?
    @Published var receivedPerSecond: Double?
    private var trafficSample: (started: String, sent: UInt64, received: UInt64, date: Date)?
    var deviceVPN: Bool {
        get { connectionMode == .internetVPN }
        set { connectionMode = newValue ? .internetVPN : .socks }
    }
    var deviceVPNAvailable: Bool { AppleVPNController.sharedContainer != nil || nativeVPN.available }
    var selectedPeer: String { !requestedPeer.isEmpty ? requestedPeer : status?.upstream_peer.isEmpty == false ? status!.upstream_peer : configuration.upstreamPeer }
    var activeSelection: Bool { status?.upstream_peer.isEmpty == false && !explicitDisconnected }
    var needsVPNRecovery: Bool { nativeVPN.state == .recoveryRequired }
    var shouldDisconnect: Bool { activeVPN || (!deviceVPN && systemProxy.enabled && internetVerified) }
    var connectionActionTitle: String { needsVPNRecovery ? "Restore networking" : shouldDisconnect ? "Disconnect" : "Connect" }
    var vpnUnavailableMessage: String { "Internet VPN is unavailable in this build. Use a build with a validated administrator helper or an Apple-signed packet extension. SOCKS and personal tunnels remain available." }
    var connectionExplanation: String {
        if deviceVPN && nativeVPN.available {
            return "Route IPv4, IPv6 and DNS through your selected server. macOS asks for administrator authorization; Disconnect restores the networking changes owned by Meshway."
        }
        return connectionMode.explanation
    }
    var selectedServer: Server? { servers.first { $0.peer_id == selectedPeer } }
    var selectedServerName: String {
        if let server = selectedServer, !server.label.isEmpty { return server.label }
        return selectedPeer.isEmpty ? "Choose a server" : "Your selected server"
    }
    var peerConnected: Bool { status?.upstream_connected == true }
    var activeVPN: Bool { [.connected, .connecting, .reasserting].contains(vpn.status) || nativeVPN.active }

    func chooseMode(_ mode: ConnectionMode) {
        guard !busy, !activeVPN, !needsVPNRecovery, mode != .internetVPN || deviceVPNAvailable else { return }
        connectionMode = mode
        UserDefaults.standard.set(mode.rawValue, forKey: "connectionMode")
    }

    func toggleConnection() async {
        if needsVPNRecovery {
            guard !busy else { return }
            busy = true; defer { busy = false }
            if await restoreDeviceVPN() { message = "Meshway’s device VPN networking was restored. Click Connect to retry." }
            return
        }
        if activeVPN { await disconnect(); return }
        if !deviceVPN && systemProxy.enabled && internetVerified { await disconnect(); return }
        if connectionMode == .personalTunnel, !configuration.personalTransportEnabled, !configuration.bootstrapPeers.contains(where: { $0.hasPrefix("/ip4/127.0.0.1/tcp/") }) {
            error = "Configure your personal server’s SSH alias and pinned peer in Settings first."; return
        }
        if status == nil { explicitDisconnected = false; await start() }
        guard status != nil else { return }
        // Reopening may attach to a still-running owned daemon before the
        // periodic monitor has loaded its approved server and proxy journal.
        await refresh(showError: false)
        guard status != nil else { return }
        if configuration.personalTransportEnabled && status?.transport?.authenticated != true {
            busy = true; message = "Opening your private SSH connection and verifying the server…"
            for _ in 0..<30 {
                if let live = try? await DaemonAPI(configuration: configuration).status() { status = live }
                if status?.transport?.authenticated == true { break }
                try? await Task.sleep(for: .milliseconds(500))
            }
            busy = false
            guard status?.transport?.authenticated == true else { error = "Your private SSH connection is not ready. Check that your server is online, then retry."; technicalError = status?.transport?.error; return }
            await refresh()
        }
        if connectionMode == .internetVPN {
            guard deviceVPNAvailable else { error = vpnUnavailableMessage; return }
            guard let server = selectedServer else { error = "Choose an available internet exit in Servers first."; return }
            await selectServer(server)
        } else if let server = selectedServer, !peerConnected {
            await selectServer(server)
        } else if selectedPeer.isEmpty {
            message = "Meshway is ready. Choose an approved server in Servers to connect."
        }
        if peerConnected && canEnableSystemProxy {
            if systemProxy.enabled && !internetVerified { await verifyExistingSystemProxyInternet() }
            else if !systemProxy.enabled { await setSystemProxy(true) }
        }
    }
    let vpn = AppleVPNController()
    let nativeVPN = NativeMacVPNController()
    let systemProxy: SystemProxyController
    @Published var showSettings = false
    @Published var showPeerDetails = false
    let configURL: URL
    private var process: Process?
    private var logHandle: FileHandle?
    private var monitor: Task<Void, Never>?
    private var handledStartSOCKS = false

    func startSOCKSIfRequested() {
        guard !handledStartSOCKS, CommandLine.arguments.contains("--start-socks") else { return }
        handledStartSOCKS = true
        startupDiagnostics("requested")
        // Explicit command-line opt-in uses the same owned lifecycle as Start.
        // It starts only the local SOCKS peer node, never a device VPN.
        deviceVPN = false
        if CommandLine.arguments.contains("--allow-storage-access") { allowStorageInteraction = true; startupDiagnostics("explicit_storage_access_requested") }
        Task {
            // Explicit repair launch restores the owned journal before ending
            // an existing authenticated daemon. It never leaves a stale proxy
            // pointing at a deliberately stopped SOCKS listener.
            if CommandLine.arguments.contains("--restart-owned-daemon") {
                guard await restartExistingDaemonForExplicitRepair() else { return }
            }
            await start()
            if CommandLine.arguments.contains("--connect-system-proxy") { await toggleConnection() }
        }
    }

    private func restartExistingDaemonForExplicitRepair() async -> Bool {
        message = "Restoring your previous proxy settings before updating Meshway. Complete any macOS network-preferences authorization prompt."
        startupDiagnostics("restoring_previous_proxy")
        guard await restoreSystemProxy() else { startupDiagnostics("proxy_restoration_required"); return false }
        do {
            let api = try DaemonAPI(configuration: configuration)
            guard (try? await api.status()) != nil else { return true }
            try await api.shutdown()
            for _ in 0..<30 {
                if (try? await api.status()) == nil { status = nil; return true }
                try? await Task.sleep(for: .milliseconds(100))
            }
            error = "The previous Meshway node is still stopping. Your previous proxy settings were restored; retry shortly."
        } catch { self.error = error.localizedDescription }
        startupDiagnostics("previous_daemon_still_running")
        return false
    }

    init() {
        let directory = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appendingPathComponent("Meshway")
        configURL = directory.appendingPathComponent("config.json")
        systemProxy = SystemProxyController(directory: directory)
        // A secure random failure must produce a visible error, not an insecure fallback token.
        do { configuration = try Configuration.makeDefault(stateDirectory: directory.appendingPathComponent("node").path) }
        catch { fatalError("Secure configuration generation failed: \(error.localizedDescription)") }
        let bundled = Bundle.main.url(forResource: "meshway", withExtension: nil)?.path ?? ""
        // A packaged app may move from a mounted DMG to Applications. A
        // remembered bundled path must not keep launching from the old volume.
        let savedExecutable = UserDefaults.standard.string(forKey: "daemonExecutable")
        if let savedExecutable, !savedExecutable.hasSuffix("/Meshway.app/Contents/Resources/meshway"), FileManager.default.isExecutableFile(atPath: savedExecutable) {
            executablePath = savedExecutable
        } else { executablePath = bundled }
        if FileManager.default.fileExists(atPath: configURL.path) {
            do {
                configuration = try Configuration(data: Data(contentsOf: configURL))
            } catch {
                self.error = "Could not read configuration: \(error.localizedDescription). Existing file has been preserved."
            }
        } else {
            do { try configuration.save(to: configURL) }
            catch { self.error = error.localizedDescription }
        }
        if let shared = AppleVPNController.sharedContainer {
            let previous = URL(fileURLWithPath: configuration.stateDirectory)
            let destination = shared.appendingPathComponent("node")
            configuration.stateDirectory = destination.path
            do {
                if FileManager.default.fileExists(atPath: previous.path), !FileManager.default.fileExists(atPath: destination.path) {
                    try StateMigration.copyStoppedLegacyState(from: previous, to: destination)
                }
                configuration.stateDirectory = destination.path
                try configuration.save(to: configURL)
            } catch { self.error = "Could not migrate private state into the VPN app group: \(error.localizedDescription)"; try? configuration.save(to: configURL) }
        }
        if let stored = UserDefaults.standard.string(forKey: "connectionMode"), let mode = ConnectionMode(rawValue: stored), mode != .internetVPN || deviceVPNAvailable {
            connectionMode = mode
        }
        bootstrapText = configuration.bootstrapPeers.joined(separator: "\n")
        allowedPeersText = configuration.allowedPeers.joined(separator: "\n")
        monitor = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh(showError: false)
                try? await Task.sleep(for: .seconds(3))
            }
        }
    }

    private func startupDiagnostics(_ phase: String) {
        guard CommandLine.arguments.contains("--start-socks") else { return }
        let values: [String: Any] = ["phase": phase, "storage_access_needed": storageAccessNeeded, "busy": busy, "owned_daemon_running": runningOwnedProcess, "timestamp": ISO8601DateFormatter().string(from: Date())]
        let path = configURL.deletingLastPathComponent().appendingPathComponent("startup-diagnostics.json")
        do { try JSONSerialization.data(withJSONObject: values, options: [.sortedKeys]).write(to: path, options: .atomic); try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path) } catch { }
    }

    func allowStorageAccess() {
        startupDiagnostics("storage_access_button_clicked")
        guard process == nil, !runningOwnedProcess else { message = "Your peer node is already running."; return }
        // A failed storage step has no child. Recover a stale UI busy flag
        // while preserving the guard against duplicate live daemon starts.
        if storageAccessNeeded { busy = false }
        guard !busy else { message = "Meshway is still opening protected storage. Complete the macOS permission dialog first."; return }
        allowStorageInteraction = true; storageAccessNeeded = false
        Task { await start() }
    }

    func save() throws {
        configuration.bootstrapPeers = lines(bootstrapText)
        configuration.allowedPeers = lines(allowedPeersText)
        try configuration.save(to: configURL)
        UserDefaults.standard.set(executablePath, forKey: "daemonExecutable")
    }

    private func lines(_ text: String) -> [String] {
        text.split(whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
    }

    func saveSettings() {
        do { try save(); error = nil; message = "Settings saved. They apply when the daemon starts." }
        catch { self.error = error.localizedDescription }
    }

    func selectExecutable() {
        let panel = NSOpenPanel()
        panel.title = "Choose the Meshway daemon executable"
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        if panel.runModal() == .OK, let url = panel.url { executablePath = url.path }
    }

    func openConfiguration() { NSWorkspace.shared.activateFileViewerSelecting([configURL]) }

    private func runAccountCommand(_ command: String, file: URL, replaceAccount: Bool = false) async throws {
        try save()
        guard process == nil, status == nil, !activeVPN, nativeVPN.state != .recoveryRequired else { throw ClientError.invalidConfiguration("Stop the daemon and restore device VPN before exporting or importing your recovery key.") }
        guard FileManager.default.isExecutableFile(atPath: executablePath) else { throw ClientError.invalidConfiguration("Choose the Meshway daemon executable in advanced settings first.") }
        let executable = executablePath
        let config = configURL.path
        try await Task.detached {
            func run(_ arguments: [String]) throws {
                let child = Process()
                child.executableURL = URL(fileURLWithPath: executable)
                child.arguments = arguments
                child.environment = ProcessInfo.processInfo.environment.merging(["MESHWAY_STATE_KEY": try NativeStateKey.loadOrCreate().base64EncodedString()]) { _, value in value }
                let pipe = Pipe()
                child.standardOutput = pipe
                child.standardError = pipe
                try child.run()
                // Drain while it runs so a verbose failure cannot fill the pipe.
                let output = pipe.fileHandleForReading.readDataToEndOfFile()
                child.waitUntilExit()
                guard child.terminationStatus == 0 else { throw ClientError.invalidConfiguration(String(decoding: output.prefix(2048), as: UTF8.self)) }
            }
            if command == "export" { try run(["account", "create", "--config", config]) }
            var arguments = ["account", command, "--config", config, (command == "export" || command == "rotate") ? "--output" : "--input", file.path]
            if replaceAccount { arguments.append("--force") }
            try run(arguments)
        }.value
    }

    func exportRecovery() async {
        let panel = NSSavePanel()
        panel.title = "Save your private Meshway recovery key"
        panel.nameFieldStringValue = "meshway-recovery.json"
        panel.message = "Anyone with this file can use your account. Save it privately; Meshway cannot recover a lost key. Choose a new file."
        guard panel.runModal() == .OK, let file = panel.url else { return }
        busy = true
        defer { busy = false }
        do { try await runAccountCommand("export", file: file); error = nil; message = "Recovery key saved to your chosen private file." }
        catch { self.error = error.localizedDescription }
    }

    func rotateRecovery() async {
        let alert = NSAlert()
        alert.messageText = "Replace your account recovery key?"
        alert.informativeText = "Stop Meshway and its device VPN first. This creates a new account ID and clears local device/history records. Save the new recovery backup, then import it on every trusted device and personal server before reconnecting. The old key still controls the old account."
        alert.addButton(withTitle: "Choose new private backup…"); alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let panel = NSSavePanel(); panel.title = "Save your new Meshway recovery key"; panel.nameFieldStringValue = "meshway-new-recovery.json"
        guard panel.runModal() == .OK, let file = panel.url else { return }
        busy = true; defer { busy = false }
        do {
            try await runAccountCommand("rotate", file: file)
            accountText = "New account created. Import this new backup on your other devices and personal servers."
            message = "New recovery key saved. Your account ID has changed."; error = nil
        } catch { self.error = error.localizedDescription }
    }

    func revokeDevice() async {
        let peer = revokedPeer.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !peer.isEmpty else { return }
        let alert = NSAlert(); alert.messageText = "Revoke this account device?"
        alert.informativeText = "The signed device revocation will propagate when trusted devices synchronize. A stolen recovery key can still enroll new devices; rotate the account if that key is compromised."
        alert.addButton(withTitle: "Revoke device"); alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        do { try await DaemonAPI(configuration: configuration).revokeDevice(peerID: peer); await refreshAccount(); message = "Device revoked on this account." }
        catch { self.error = error.localizedDescription }
    }

    func clearHistory() async {
        let alert = NSAlert(); alert.messageText = "Delete connection history?"
        alert.informativeText = syncHistoryDeletion ? "This creates a signed deletion record. Trusted devices apply it on their next account sync. Device membership remains available." : "Only history stored on this device will be deleted. Other devices retain their copies."
        alert.addButton(withTitle: "Delete history"); alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        do { try await DaemonAPI(configuration: configuration).clearHistory(synchronize: syncHistoryDeletion); await refreshAccount(); message = "History deleted." }
        catch { self.error = error.localizedDescription }
    }

    func importRecovery() async {
        let panel = NSOpenPanel()
        panel.title = "Join your account on this device"
        panel.message = "Choose your private Meshway recovery file. Replacing an existing local account requires a separate confirmation."
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        guard panel.runModal() == .OK, let file = panel.url else { return }
        var replace = false
        if FileManager.default.fileExists(atPath: URL(fileURLWithPath: configuration.stateDirectory).appendingPathComponent("account.seed").path) {
            let alert = NSAlert()
            alert.messageText = "Replace this device's local account?"
            alert.informativeText = "Importing another recovery key replaces the current account and clears local device/history records. This device keeps its peer identity. Save your existing recovery key first if you need it."
            alert.addButton(withTitle: "Replace account")
            alert.addButton(withTitle: "Cancel")
            guard alert.runModal() == .alertFirstButtonReturn else { return }
            replace = true
        }
        busy = true
        defer { busy = false }
        do { try await runAccountCommand("import", file: file, replaceAccount: replace); error = nil; message = "Recovery key imported. Start Meshway to enroll this device." }
        catch { self.error = error.localizedDescription }
    }

    func start() async {
        guard !busy, process == nil else { return }
        busy = true
        defer { busy = false; startupDiagnostics(storageAccessNeeded ? "storage_permission_required" : runningOwnedProcess ? "daemon_running" : "start_finished") }
        do {
            try save()
            let api = try DaemonAPI(configuration: configuration)
            if let existing = try? await api.status() {
                status = existing
                message = "A daemon is already running with this configuration. Meshway is viewing its status."
                await resumeExistingSystemProxy(for: existing)
                return
            }
            guard FileManager.default.isExecutableFile(atPath: executablePath) else {
                throw ClientError.invalidConfiguration("Choose a built Meshway daemon executable first, or install the packaged app.")
            }
            let logURL = configURL.deletingLastPathComponent().appendingPathComponent("daemon.log")
            if !FileManager.default.fileExists(atPath: logURL.path) {
                FileManager.default.createFile(atPath: logURL.path, contents: nil, attributes: [.posixPermissions: 0o600])
            }
            let handle = try FileHandle(forWritingTo: logURL)
            try handle.seekToEnd()
            let child = Process()
            child.executableURL = URL(fileURLWithPath: executablePath)
            child.arguments = ["daemon", "--config", configURL.path]
            message = "Opening this device’s protected Meshway storage…"
            startupDiagnostics("opening_protected_storage")
            let allowInteraction = allowStorageInteraction
            let stateKey = try await Task.detached { try NativeStateKey.loadOrCreate(allowInteraction: allowInteraction) }.value
            startupDiagnostics("protected_storage_opened")
            child.environment = ProcessInfo.processInfo.environment.merging(["MESHWAY_STATE_KEY": stateKey.base64EncodedString()]) { _, value in value }
            child.standardOutput = handle
            child.standardError = handle
            child.terminationHandler = { [weak self] child in
                let exitCode = child.terminationStatus
                Task { @MainActor [weak self] in
                    guard let self else { return }
                    self.process = nil
                    self.runningOwnedProcess = false
                    self.status = nil
                    try? self.logHandle?.close()
                    self.logHandle = nil
                    self.message = "Daemon stopped (exit \(exitCode))."
                    if self.nativeVPN.state != .disconnected { _ = await self.restoreDeviceVPN() }
                    if self.systemProxy.enabled || self.systemProxy.needsRecovery { _ = await self.restoreSystemProxy() }
                    if exitCode != 0 { self.error = "The peer node stopped unexpectedly. Your previous proxy settings were restored if possible; reconnect to retry." }
                }
            }
            logHandle = handle
            process = child
            do { try child.run() }
            catch { process = nil; logHandle = nil; try? handle.close(); throw error }
            runningOwnedProcess = true
            error = nil
            message = "Starting the encrypted peer node…"
            for _ in 0..<20 {
                if !child.isRunning { break }
                if let live = try? await api.status() {
                    status = live
                    message = "Daemon running. Configure applications to use the SOCKS address below."
                    return
                }
                try await Task.sleep(for: .milliseconds(250))
            }
            throw ClientError.invalidConfiguration("Daemon did not become ready within 5 seconds. Inspect daemon.log, then stop and retry.")
        } catch {
            technicalError = error.localizedDescription
            if error.localizedDescription.contains("Keychain") {
                storageAccessNeeded = true
                startupDiagnostics("storage_permission_required")
                self.error = "Meshway needs permission to open its own protected storage after this app update. Choose Allow storage access; macOS may ask you to approve Meshway."
            } else { self.error = error.localizedDescription }
        }
    }

    func stop() async {
        guard let child = process, !busy else { return }
        internetVerified = false; observedExitIP = nil
        guard await restoreDeviceVPN() else { return }
        guard await restoreSystemProxy() else { return }
        busy = true
        defer { busy = false }
        message = "Stopping daemon…"
        do { try await DaemonAPI(configuration: configuration).shutdown() }
        catch { if child.isRunning { child.terminate() } }
        for _ in 0..<20 {
            if !child.isRunning { return }
            try? await Task.sleep(for: .milliseconds(100))
        }
        if child.isRunning { child.terminate() }
    }

    func terminateOwnedProcess() {
        monitor?.cancel()
        if let process, process.isRunning { process.terminate() }
    }

    func refresh(showError: Bool = true) async {
        do {
            let live = try await DaemonAPI(configuration: configuration).status()
            let now = Date()
            if let sample = trafficSample, sample.started == live.started_at, live.bytes_sent >= sample.sent, live.bytes_received >= sample.received {
                let elapsed = now.timeIntervalSince(sample.date)
                if elapsed > 0 {
                    sentPerSecond = Double(live.bytes_sent - sample.sent) / elapsed
                    receivedPerSecond = Double(live.bytes_received - sample.received) / elapsed
                }
            } else { sentPerSecond = nil; receivedPerSecond = nil }
            trafficSample = (live.started_at, live.bytes_sent, live.bytes_received, now)
            status = live
            await resumeExistingSystemProxy(for: live)
            if systemProxy.enabled {
                let stillApplied = await systemProxy.verifyStillAppliedAsync()
                if !stillApplied {
                    internetVerified = false; observedExitIP = nil
                    message = "System proxy changed outside Meshway. Your peer is still ready, but supported apps may now use another route."
                }
            }
            do {
                servers = try await DaemonAPI(configuration: configuration).servers()
                catalogMessage = servers.isEmpty ? "No servers yet. Add an invitation address from a friend or your server." : "Choose a server to connect."
            } catch { servers = []; catalogMessage = "Server list unavailable: \(error.localizedDescription)" }
            if showError { error = nil }
        } catch {
            status = nil
            trafficSample = nil; sentPerSecond = nil; receivedPerSecond = nil
            if systemProxy.enabled { internetVerified = false; observedExitIP = nil }
            if showError { self.error = "Daemon unavailable: \(error.localizedDescription)" }
        }
    }

    func refreshAccount() async {
        do {
            accountText = try await DaemonAPI(configuration: configuration).accountSnapshot()
            error = nil
        } catch { self.error = "Account unavailable: \(error.localizedDescription)" }
    }

    func syncAccount() async {
        let peer = accountSyncPeer.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !peer.isEmpty, !busy else { return }
        busy = true
        defer { busy = false }
        do {
            try await DaemonAPI(configuration: configuration).syncAccount(peerID: peer)
            await refreshAccount()
            message = "Account records synchronized with the chosen peer."
        } catch { self.error = error.localizedDescription }
    }

    func selectServer(_ server: Server) async {
        guard !busy, !activeVPN, !needsVPNRecovery else { return }
        requestedPeer = server.peer_id
        message = "Connecting to \(server.label.isEmpty ? "your server" : server.label)…"
        error = nil
        busy = true
        defer { busy = false }
        do {
            if deviceVPN {
                guard deviceVPNAvailable else { throw ClientError.invalidConfiguration(vpnUnavailableMessage) }
                guard await restoreSystemProxy() else { return }
                if nativeVPN.available {
                    try await DaemonAPI(configuration:configuration).select(server:server)
                    try save()
                    try await nativeVPN.start(configuration:configuration,configURL:configURL,peerID:server.peer_id,peerAddresses:(server.addresses ?? []) + [server.address])
                    message = nativeVPN.state == .connected ? "Device VPN capture is active. Your selected server is connected." : "Device VPN capture is retained while your server reconnects."
                    return
                }
                // The foreground core and extension use the same identity store;
                // release the foreground lock before the provider opens it.
                guard let child = process else { throw ClientError.invalidConfiguration("Start Meshway from this app before selecting a device VPN server.") }
                try await DaemonAPI(configuration: configuration).shutdown()
                for _ in 0..<50 {
                    if !child.isRunning { break }
                    try await Task.sleep(for: .milliseconds(100))
                }
                guard !child.isRunning else { throw ClientError.invalidConfiguration("The foreground node is still stopping. Retry after it stops.") }
                try save()
                try await vpn.connect(config: configuration.encoded(), peerID: server.peer_id, invitation: server.address)
                message = "Connecting device VPN to \(server.label)…"
                return
            }
            try await DaemonAPI(configuration: configuration).select(server: server)
            explicitDisconnected = false
            message = "Selected \(server.label). Applications can use the Meshway SOCKS connection."
            error = nil
            await refresh()
        } catch {
            technicalError = error.localizedDescription
            if deviceVPN { self.error = "Device VPN could not start. " + error.localizedDescription }
            else { self.error = configuration.personalTransportEnabled || server.address.hasPrefix("/ip4/127.0.0.1/") ? "Your private SSH connection is offline. Reconnect your personal server, then click Connect again." : "Could not connect to this server. Check that it is online and your device is approved." }
        }
    }

    func disconnect() async {
        internetVerified = false; observedExitIP = nil
        guard await restoreDeviceVPN() else { return }
        guard await restoreSystemProxy() else { return }
        if [.connected, .connecting, .reasserting].contains(vpn.status) {
            vpn.disconnect(); message = "Disconnecting device VPN…"; return
        }
        do {
            if !selectedPeer.isEmpty { requestedPeer = selectedPeer }
            try await DaemonAPI(configuration: configuration).disconnect()
            explicitDisconnected = true
            message = "Disconnected. Click Connect to reconnect to your chosen server."
            await refresh()
        }
        catch { self.error = error.localizedDescription }
    }

    var canEnableSystemProxy: Bool {
        status != nil && peerConnected && (status?.transport?.kind != "ssh" || status?.transport?.authenticated == true) && selectedServer?.can_connect == true && selectedServer?.roles.exit == true && !activeVPN && !needsVPNRecovery && !deviceVPN && !(status?.socks_listen.isEmpty ?? true)
    }

    private func resumeExistingSystemProxy(for live: DaemonStatus) async {
        guard systemProxy.needsRecovery, !systemProxy.enabled, !activeVPN,
              live.socks_listen == configuration.socksListen,
              let endpoint = URLComponents(string: "socks://" + live.socks_listen),
              endpoint.user == nil, endpoint.password == nil, endpoint.path.isEmpty,
              endpoint.query == nil, endpoint.fragment == nil,
              let rawHost = endpoint.host, let port = endpoint.port else { return }
        let host = rawHost.replacingOccurrences(of: "[", with: "").replacingOccurrences(of: "]", with: "")
        if await systemProxy.resumeOwnAppliedJournalAsync(host: host, port: port) {
            internetVerified = false; observedExitIP = nil
            message = "Your existing Meshway system proxy is still applied. Connect to verify secure internet access."
        }
    }

    @discardableResult
    func restoreSystemProxy() async -> Bool {
        do { try await systemProxy.restoreAsync(); internetVerified = false; observedExitIP = nil; return true }
        catch { self.error = error.localizedDescription; return false }
    }
    @discardableResult
    func restoreDeviceVPN() async -> Bool {
        if nativeVPN.state != .disconnected {
            do { try await nativeVPN.stop() }
            catch { self.error = error.localizedDescription; return false }
        }
        if [.connected,.connecting,.reasserting].contains(vpn.status) {
            vpn.disconnect()
            for _ in 0..<50 {
                if ![.connected,.connecting,.reasserting,.disconnecting].contains(vpn.status) { return true }
                try? await Task.sleep(for:.milliseconds(100))
            }
            error = "The Apple VPN extension is still stopping. Wait for it to disconnect before closing Meshway."
            return false
        }
        return true
    }
    func restoreForQuit() async -> Bool {
        guard await restoreDeviceVPN() else { return false }
        return await restoreSystemProxy()
    }

    func setSystemProxy(_ enabled: Bool) async {
        guard !busy else { return }
        if !enabled { _ = await restoreSystemProxy(); return }
        if systemProxy.needsRecovery { guard await restoreSystemProxy() else { return } }
        guard canEnableSystemProxy, let listener = status?.socks_listen,
              let url = URLComponents(string: "socks://" + listener), let port = url.port,
              let host = url.host else { error = "Connect to an approved exit with a healthy local SOCKS listener before enabling System proxy."; return }
        let alert = NSAlert()
        alert.messageText = "Use Meshway as your Mac’s SOCKS proxy?"
        alert.informativeText = "Supported apps will use Meshway instead of the current HTTP/HTTPS/PAC proxy. This changes SOCKS settings on enabled network services; it does not capture every app like Internet VPN. macOS may request administrator authorization. Meshway restores previous settings when you disconnect, stop or quit."
        alert.addButton(withTitle: "Enable System proxy"); alert.addButton(withTitle: "Cancel")
        if !CommandLine.arguments.contains("--connect-system-proxy") {
            guard alert.runModal() == .alertFirstButtonReturn else { return }
        }
        busy = true; defer { busy = false }
        do {
            message = "Checking secure internet access through your server…"
            try await checkSOCKSInternet(listener: listener)
            try await systemProxy.enableAsync(host: host.replacingOccurrences(of: "[", with: "").replacingOccurrences(of: "]", with: ""), port: port)
            do {
                observedExitIP = try await checkDefaultProxyInternet()
                internetVerified = true
                message = "Supported apps can use Meshway. Secure HTTPS through the macOS proxy passed."; error = nil
            } catch { _ = await restoreSystemProxy(); throw ClientError.invalidConfiguration("macOS proxy internet access did not pass its check. The previous proxy was restored. " + error.localizedDescription) }
        }
        catch { self.error = error.localizedDescription }
    }

    private func verifyExistingSystemProxyInternet() async {
        guard !busy, canEnableSystemProxy, systemProxy.enabled else { return }
        busy = true; defer { busy = false }
        internetVerified = false; observedExitIP = nil
        message = "Verifying secure internet access through your existing Meshway proxy…"
        do {
            guard await systemProxy.verifyStillAppliedAsync() else {
                throw ClientError.invalidConfiguration("The system proxy changed outside Meshway. Previous settings remain saved for explicit restoration.")
            }
            let ip = try await checkDefaultProxyInternet()
            guard await systemProxy.verifyStillAppliedAsync() else {
                throw ClientError.invalidConfiguration("The system proxy changed during the internet check. Previous settings remain saved for explicit restoration.")
            }
            observedExitIP = ip; internetVerified = true
            message = "Supported apps can use Meshway. Secure HTTPS through the existing macOS proxy passed."; error = nil
        } catch {
            // Resuming and testing an existing proxy never reapplies or restores
            // preferences, including after a temporary server outage.
            self.error = "Existing proxy internet access did not pass its check. " + error.localizedDescription
        }
    }

    private func checkDefaultProxyInternet() async throws -> String {
        let before = try await DaemonAPI(configuration: configuration).status()
        let session = URLSession(configuration: .ephemeral)
        defer { session.invalidateAndCancel() }
        var request = URLRequest(url: URL(string: "https://example.com/")!); request.timeoutInterval = 15
        let (data, response) = try await session.data(for: request)
        let after = try await DaemonAPI(configuration: configuration).status()
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode), !data.isEmpty,
              after.bytes_received > before.bytes_received, after.bytes_sent > before.bytes_sent else {
            throw ClientError.invalidConfiguration("The macOS HTTPS request was not observed through Meshway. Check whether another app is overriding the system proxy.")
        }
        var ipRequest = URLRequest(url: URL(string: "https://api.ipify.org?format=json")!); ipRequest.timeoutInterval = 15
        let (ipData, ipResponse) = try await session.data(for: ipRequest)
        struct IPResult: Decodable { let ip: String }
        guard let ipHTTP = ipResponse as? HTTPURLResponse, (200..<300).contains(ipHTTP.statusCode), ipData.count <= 4096 else {
            throw ClientError.invalidConfiguration("The exit IP check was unavailable.")
        }
        let ip = try JSONDecoder().decode(IPResult.self, from: ipData).ip
        guard Self.validPublicIP(ip) else { throw ClientError.invalidConfiguration("The exit IP response was invalid.") }
        return ip
    }

    nonisolated private static func validPublicIP(_ value: String) -> Bool {
        var ipv4 = in_addr(); var ipv6 = in6_addr()
        return value.withCString { inet_pton(AF_INET, $0, &ipv4) == 1 || inet_pton(AF_INET6, $0, &ipv6) == 1 }
    }

    func openBrowserIPCheck() {
        var url = URLComponents(string: "https://api64.ipify.org")!
        url.queryItems = [URLQueryItem(name: "format", value: "json"), URLQueryItem(name: "meshway_check", value: String(Int(Date().timeIntervalSince1970)))]
        if let url = url.url { NSWorkspace.shared.open(url) }
    }

    func verifyExitIP() async {
        guard !busy, peerConnected, let listener = status?.socks_listen, !listener.isEmpty else { error = "Connect your peer node to an approved server first."; return }
        busy = true; defer { busy = false }
        do {
            let ip = try await Task.detached {
                let child = Process(); let output = Pipe(); let failure = Pipe()
                child.executableURL = URL(fileURLWithPath: "/usr/bin/curl")
                child.arguments = ["--fail", "--silent", "--show-error", "--max-time", "15", "--max-filesize", "4096", "--socks5-hostname", listener, "--noproxy", "", "https://api.ipify.org?format=json"]
                child.standardOutput = output; child.standardError = failure
                try child.run(); let data = output.fileHandleForReading.readDataToEndOfFile(); let details = failure.fileHandleForReading.readDataToEndOfFile(); child.waitUntilExit()
                guard child.terminationStatus == 0, data.count <= 4096 else { throw ClientError.invalidConfiguration("The exit-IP check failed. " + String(decoding: details.prefix(256), as: UTF8.self)) }
                struct IPResult: Decodable { let ip: String }
                let ip = try JSONDecoder().decode(IPResult.self, from: data).ip
                guard Self.validPublicIP(ip) else { throw ClientError.invalidConfiguration("The public IP response was invalid.") }
                return ip
            }.value
            observedExitIP = ip; message = "Observed public IP through your selected SOCKS exit: " + ip; error = nil
        } catch { self.error = error.localizedDescription }
    }

    private func checkSOCKSInternet(listener: String) async throws {
        try await Task.detached {
            let child = Process(); let output = Pipe(); let failure = Pipe()
            child.executableURL = URL(fileURLWithPath: "/usr/bin/curl")
            child.arguments = ["--fail", "--silent", "--show-error", "--max-time", "15", "--socks5-hostname", listener, "--noproxy", "", "--output", "/dev/null", "https://example.com/"]
            child.standardOutput = output; child.standardError = failure
            try child.run(); let text = failure.fileHandleForReading.readDataToEndOfFile(); child.waitUntilExit()
            guard child.terminationStatus == 0 else { throw ClientError.invalidConfiguration("Secure internet through your server is not ready. Your current proxy was preserved. " + String(decoding: text.prefix(256), as: UTF8.self)) }
        }.value
    }

    func connect() async {
        let address = connectAddress.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !address.isEmpty, !busy else { return }
        busy = true
        defer { busy = false }
        do {
            try await DaemonAPI(configuration: configuration).connect(address: address)
            error = nil
            message = "Peer connection established."
            await refresh()
        } catch { self.error = error.localizedDescription }
    }
}


enum ConnectionMode: String, CaseIterable, Identifiable {
    case internetVPN, socks, personalTunnel
    var id: String { rawValue }
    var title: String {
        switch self { case .internetVPN: return "Internet VPN"; case .socks: return "SOCKS proxy"; case .personalTunnel: return "Personal tunnel" }
    }
    var icon: String {
        switch self { case .internetVPN: return "shield.lefthalf.filled"; case .socks: return "app.connected.to.app.below.fill"; case .personalTunnel: return "lock.shield.fill" }
    }
    var subtitle: String {
        switch self { case .internetVPN: return "Entire device · TUN"; case .socks: return "Apps you choose"; case .personalTunnel: return "Your own private server" }
    }
    var explanation: String {
        switch self {
        case .internetVPN: return "Route IPv4, IPv6 and DNS through an approved Meshway exit using native device VPN."
        case .socks: return "Connect your browser or another app to the local SOCKS5 address. Meshway encrypts traffic to your selected server."
        case .personalTunnel: return "Use your own same-account Meshway server through a private SSH connection. Applications connect through the local SOCKS5 address."
        }
    }
}

private enum DashboardPage: String, CaseIterable, Identifiable {
    case dashboard = "Dashboard", servers = "Servers", connections = "Connections", tools = "Tools", settings = "Settings"
    var id: String { rawValue }
    var icon: String {
        switch self { case .dashboard: return "square.grid.2x2.fill"; case .servers: return "server.rack"; case .connections: return "arrow.left.arrow.right"; case .tools: return "wrench.and.screwdriver"; case .settings: return "slider.horizontal.3" }
    }
    var subtitle: String {
        switch self {
        case .dashboard: return "Your network, at a glance."
        case .servers: return "Connect to an approved peer or your own server."
        case .connections: return "Live peers and measured traffic from your node."
        case .tools: return "Your account, devices and private recovery backup."
        case .settings: return "Fine-tune your node and decide what you share."
        }
    }
}

private enum MeshwayTheme {
    static let background = Color(red: 0.075, green: 0.076, blue: 0.082)
    static let sidebar = Color(red: 0.105, green: 0.108, blue: 0.118)
    static let card = Color(red: 0.13, green: 0.133, blue: 0.145)
    static let inset = Color(red: 0.10, green: 0.103, blue: 0.113)
    static let accent = Color(red: 211 / 255, green: 1, blue: 115 / 255)
    static let green = Color(red: 0.40, green: 0.84, blue: 0.66)
    static let muted = Color(red: 0.59, green: 0.60, blue: 0.64)
    static let border = Color.white.opacity(0.075)
}

private struct Panel<Content: View>: View {
    let content: Content
    init(@ViewBuilder content: () -> Content) { self.content = content() }
    var body: some View {
        content.padding(22).frame(maxWidth: .infinity, alignment: .leading)
            .background(MeshwayTheme.card, in: RoundedRectangle(cornerRadius: 18))
            .overlay(RoundedRectangle(cornerRadius: 18).stroke(MeshwayTheme.border, lineWidth: 1))
    }
}

private struct MetricTile: View {
    let title: String
    let value: String
    let detail: String
    let icon: String
    var body: some View {
        Panel {
            VStack(alignment: .leading, spacing: 13) {
                HStack { Text(title).font(.callout).foregroundStyle(MeshwayTheme.muted); Spacer(); Image(systemName: icon).foregroundStyle(MeshwayTheme.accent) }
                Text(value).font(.system(size: 25, weight: .semibold, design: .rounded)).monospacedDigit()
                Text(detail).font(.caption).foregroundStyle(MeshwayTheme.muted)
            }
        }
    }
}

struct ContentView: View {
    @ObservedObject var model: AppModel
    @ObservedObject private var vpn: AppleVPNController
    @ObservedObject private var systemProxy: SystemProxyController
    @ObservedObject private var nativeVPN: NativeMacVPNController
    @State private var page: DashboardPage = .dashboard
    init(model: AppModel) { self.model = model; vpn = model.vpn; systemProxy = model.systemProxy; nativeVPN = model.nativeVPN }
    private var connected: Bool { model.connectionMode == .internetVPN ? vpn.status == .connected || nativeVPN.state == .connected : model.peerConnected && (model.status?.transport?.kind != "ssh" || model.status?.transport?.authenticated == true) && model.systemProxy.enabled && model.internetVerified }
    private var connectionTitle: String {
        if model.busy { return "Working on your connection…" }
        if model.needsVPNRecovery { return "Device VPN cleanup needs attention" }
        if model.connectionMode == .internetVPN {
            if !model.deviceVPNAvailable { return "Device VPN unavailable in this build" }
            if nativeVPN.state == .authorizing { return "Waiting for macOS authorization" }
            if nativeVPN.state == .connecting { return "Configuring device VPN" }
            if nativeVPN.state == .connected { return "Device VPN capture active" }
            if nativeVPN.state == .reconnecting { return "VPN capture retained · reconnecting" }
            if nativeVPN.state == .restoring { return "Restoring your Mac networking" }
            if nativeVPN.state == .recoveryRequired { return "Device VPN cleanup needs attention" }
            if vpn.status == .reasserting { return "Reconnecting your VPN" }
            if vpn.status == .connected { return "VPN interface active" }
            if vpn.status == .connecting { return "Connecting your VPN" }
        }
        if model.explicitDisconnected { return "Disconnected from your server" }
        if model.peerConnected {
            return model.systemProxy.enabled && model.internetVerified ? "Internet ready for supported apps" : "Peer ready · proxy is off"
        }
        if model.status != nil { return model.selectedPeer.isEmpty ? "Ready to connect" : "Waiting for your server" }
        return "Your connection starts here"
    }
    private func bytes(_ value: UInt64) -> String { ByteCountFormatter.string(fromByteCount: Int64(clamping: value), countStyle: .binary) }
    private func speed(_ value: Double?) -> String {
        guard let value else { return "Measuring…" }
        return ByteCountFormatter.string(fromByteCount: Int64(min(value, Double(Int64.max))), countStyle: .binary) + "/s"
    }
    private func uptime(_ date: Date) -> String {
        guard let value = model.status?.started_at else { return "—" }
        let fractional = ISO8601DateFormatter(); fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        guard let start = fractional.date(from: value) ?? ISO8601DateFormatter().date(from: value) else { return "—" }
        let seconds = max(0, Int(date.timeIntervalSince(start)))
        return seconds >= 3600 ? String(format: "%dh %02dm", seconds / 3600, (seconds % 3600) / 60) : String(format: "%dm %02ds", seconds / 60, seconds % 60)
    }

    var body: some View {
        HStack(spacing: 0) {
            sidebar.frame(width: 220)
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    HStack(alignment: .top) {
                        VStack(alignment: .leading, spacing: 7) {
                            Text(page.rawValue).font(.system(size: 29, weight: .semibold))
                            Text(page.subtitle).font(.callout).foregroundStyle(MeshwayTheme.muted)
                        }
                        Spacer()
                        Button { Task { await model.refresh() } } label: { Image(systemName: "arrow.clockwise").font(.system(size: 14, weight: .semibold)).padding(9).background(MeshwayTheme.card, in: RoundedRectangle(cornerRadius: 10)).contentShape(Rectangle()) }
                            .buttonStyle(FullCardButtonStyle()).disabled(model.busy).help("Refresh live status")
                    }
                    if let error = model.error {
                        VStack(alignment: .leading, spacing: 8) {
                            notice(error, icon: "exclamationmark.triangle.fill", color: .orange)
                            if let details = model.technicalError { DisclosureGroup("Technical details") { Text(details).font(.system(.caption2, design: .monospaced)).textSelection(.enabled) } }
                        }
                    }
                    if model.storageAccessNeeded {
                        Button { model.allowStorageAccess() } label: {
                            HStack { Image(systemName: "key.fill"); Text("Allow Meshway storage access").fontWeight(.semibold); Spacer() }
                                .padding(16).foregroundStyle(MeshwayTheme.background).background(MeshwayTheme.accent, in: RoundedRectangle(cornerRadius: 12))
                        }.buttonStyle(FullCardButtonStyle())
                    }
                    if systemProxy.needsRecovery { HStack { notice("Previous proxy settings need restoration. Meshway will not override a later change made by another app.", icon: "exclamationmark.triangle", color: .orange); Button("Restore previous proxy") { Task { _ = await model.restoreSystemProxy() } } } }
                    if let error = systemProxy.error, model.error != error { notice(error, icon: "exclamationmark.triangle", color: .orange) }
                    switch page {
                    case .dashboard: dashboard
                    case .servers: serverList; invitation
                    case .connections: connections
                    case .tools: accountTools
                    case .settings: settings
                    }
                }.padding(32).frame(maxWidth: 1250, alignment: .leading).frame(maxWidth: .infinity)
            }
            .background(MeshwayTheme.background)
        }
        .onAppear {
            if let icon = Bundle.main.url(forResource: "Meshway", withExtension: "icns"), let image = NSImage(contentsOf: icon) { NSApplication.shared.applicationIconImage = image }
        }
        .foregroundStyle(Color.white.opacity(0.92))
        .tint(MeshwayTheme.accent)
    }

    private var sidebar: some View {
        VStack(alignment: .leading, spacing: 30) {
            HStack(spacing: 12) {
                MeshwayLogo().frame(width: 43, height: 43)
                VStack(alignment: .leading, spacing: 3) { Text("Meshway").font(.system(size: 22, weight: .bold)); Text("Your open network").font(.caption).foregroundStyle(MeshwayTheme.muted) }
            }.padding(.top, 12)
            VStack(spacing: 7) {
                ForEach(DashboardPage.allCases) { item in
                    Button { page = item } label: {
                        HStack(spacing: 12) { Image(systemName: item.icon).frame(width: 20); Text(item.rawValue).font(.system(size: 14, weight: page == item ? .semibold : .regular)); Spacer() }
                            .padding(.horizontal, 13).padding(.vertical, 13)
                            .foregroundStyle(page == item ? MeshwayTheme.accent : MeshwayTheme.muted)
                            .background(page == item ? MeshwayTheme.accent.opacity(0.10) : Color.clear, in: RoundedRectangle(cornerRadius: 11)).contentShape(Rectangle())
                    }.buttonStyle(FullCardButtonStyle())
                }
            }
            Spacer()
            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 8) { Circle().fill(model.status == nil ? MeshwayTheme.muted : MeshwayTheme.green).frame(width: 7, height: 7); Text(model.status == nil ? "Peer node stopped" : "Peer node running").font(.caption) }
                Text("Open source. Your keys.\nYour choice of connection.").font(.caption).lineSpacing(4).foregroundStyle(MeshwayTheme.muted)
                Text(model.status?.version ?? "Personal build").font(.caption2).foregroundStyle(MeshwayTheme.muted)
            }
        }.padding(20).frame(maxHeight: .infinity).background(MeshwayTheme.sidebar)
    }

    private var dashboard: some View {
        VStack(alignment: .leading, spacing: 22) {
            Panel {
                VStack(alignment: .leading, spacing: 22) {
                    HStack(spacing: 17) {
                        Image(systemName: model.connectionMode.icon).font(.system(size: 27)).foregroundStyle(connected ? MeshwayTheme.green : MeshwayTheme.accent)
                            .frame(width: 66, height: 66).background((connected ? MeshwayTheme.green : MeshwayTheme.accent).opacity(0.10), in: RoundedRectangle(cornerRadius: 20))
                        VStack(alignment: .leading, spacing: 7) {
                            HStack(spacing: 7) { Circle().fill(connected ? MeshwayTheme.green : MeshwayTheme.muted).frame(width: 6, height: 6); Text(model.connectionMode.title.uppercased()).font(.system(size: 10, weight: .semibold)).tracking(1.3).foregroundStyle(MeshwayTheme.muted) }
                            Text(connectionTitle).font(.system(size: 23, weight: .semibold))
                            Text(model.selectedServerName).font(.callout).foregroundStyle(MeshwayTheme.muted)
                        }
                        Spacer()
                        Button { Task { await model.toggleConnection() } } label: {
                            HStack(spacing: 8) { if model.busy { ProgressView().controlSize(.small) } else { Image(systemName: model.needsVPNRecovery ? "arrow.uturn.backward" : model.shouldDisconnect ? "power" : "bolt.fill") }; Text(model.connectionActionTitle).fontWeight(.semibold) }.padding(.horizontal, 23).padding(.vertical, 14)
                                .foregroundStyle(MeshwayTheme.background).background(MeshwayTheme.accent, in: RoundedRectangle(cornerRadius: 12)).contentShape(RoundedRectangle(cornerRadius: 12))
                        }
                        .buttonStyle(FullCardButtonStyle())
                        .disabled(model.busy || (model.connectionMode == .internetVPN && !model.deviceVPNAvailable && !model.needsVPNRecovery))
                    }
                    Divider().overlay(MeshwayTheme.border)
                    HStack(spacing: 10) {
                        ForEach(ConnectionMode.allCases) { mode in modeButton(mode) }
                    }
                    Text(model.connectionExplanation).font(.callout).foregroundStyle(MeshwayTheme.muted).fixedSize(horizontal: false, vertical: true)
                    if let ip = model.observedExitIP { Text("Observed exit IP: " + ip).font(.caption).foregroundStyle(MeshwayTheme.green).textSelection(.enabled) }
                    if !model.deviceVPNAvailable {
                        HStack(spacing: 7) { Image(systemName: "info.circle"); Text(model.vpnUnavailableMessage) }.font(.caption).foregroundStyle(MeshwayTheme.muted)
                    }
                    if let error = vpn.error { notice(error, icon: "exclamationmark.triangle", color: .orange) }
                    if let error = nativeVPN.error { notice(error, icon:"exclamationmark.triangle",color:.orange) }
                    HStack {
                        Text(model.message).font(.caption).foregroundStyle(MeshwayTheme.muted).fixedSize(horizontal: false, vertical: true)
                        Spacer(minLength: 14)
                        Button("Choose server") { page = .servers }.buttonStyle(.link)
                    }
                }
            }
            LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 14) {
                MetricTile(title: "Downloaded", value: model.status.map { bytes($0.bytes_received) } ?? "—", detail: speed(model.receivedPerSecond) + " · SOCKS payload + VPN packets", icon: "arrow.down")
                MetricTile(title: "Uploaded", value: model.status.map { bytes($0.bytes_sent) } ?? "—", detail: speed(model.sentPerSecond) + " · SOCKS payload + VPN packets", icon: "arrow.up")
                TimelineView(.periodic(from: .now, by: 1)) { context in MetricTile(title: "Node uptime", value: uptime(context.date), detail: "Since this peer node started", icon: "clock") }
                MetricTile(title: "Connected peers", value: model.status.map { String($0.peers.count) } ?? "—", detail: "Live encrypted peer connections", icon: "network")
            }
            Panel {
                HStack(alignment: .top, spacing: 15) {
                    Image(systemName: "app.badge.checkmark").font(.title2).foregroundStyle(MeshwayTheme.accent)
                    VStack(alignment: .leading, spacing: 7) {
                        HStack {
                            Text("Connect your apps").font(.headline)
                            Spacer()
                            Toggle("System proxy", isOn: Binding(get: { systemProxy.enabled }, set: { enabled in Task { await model.setSystemProxy(enabled) } }))
                                .toggleStyle(.switch).disabled(model.busy || (!systemProxy.enabled && !model.canEnableSystemProxy)).fixedSize()
                        }
                        Text("Set the app’s SOCKS5 proxy to \(model.status?.socks_listen.isEmpty == false ? model.status!.socks_listen : model.configuration.socksListen). Let the proxy resolve hostnames to use your server’s DNS.").font(.callout).foregroundStyle(MeshwayTheme.muted).textSelection(.enabled)
                        Text(model.deviceVPN ? "Internet VPN captures traffic directly. Meshway restores its System proxy settings before device VPN starts; no app proxy setup is required." : systemProxy.enabled ? "System proxy is enabled for supported apps. Previous SOCKS settings restore when you disconnect, stop or quit." : "Enable System proxy after connecting to an approved exit, or configure an individual app. The proxy affects apps that honor macOS SOCKS settings.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                        Text("Traffic totals measure SOCKS payload + VPN packets through this node; they are not device-wide VPN totals.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                    }
                    Spacer()
                }
            }
        }
    }

    private func modeButton(_ mode: ConnectionMode) -> some View {
        let selected = model.connectionMode == mode
        let unavailable = mode == .internetVPN && !model.deviceVPNAvailable
        return Button { model.chooseMode(mode) } label: {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 7) { Image(systemName: mode.icon); Text(mode.title).fontWeight(.semibold); Spacer(minLength: 0); if selected { Image(systemName: "checkmark.circle.fill") } }
                    .font(.system(size: 12))
                Text(unavailable ? "Unavailable in this build" : mode == .internetVPN && nativeVPN.available ? "Entire device · macOS authorization" : mode.subtitle).font(.system(size: 10)).foregroundStyle(selected ? MeshwayTheme.accent.opacity(0.8) : MeshwayTheme.muted)
            }.padding(13).frame(maxWidth: .infinity, alignment: .leading)
                .foregroundStyle(selected ? MeshwayTheme.accent : Color.white.opacity(unavailable ? 0.5 : 0.85))
                .background(selected ? MeshwayTheme.accent.opacity(0.09) : MeshwayTheme.inset, in: RoundedRectangle(cornerRadius: 12))
                .overlay(RoundedRectangle(cornerRadius: 12).stroke(selected ? MeshwayTheme.accent.opacity(0.45) : MeshwayTheme.border, lineWidth: 1)).contentShape(RoundedRectangle(cornerRadius: 12))
        }.buttonStyle(FullCardButtonStyle()).disabled(model.busy || model.activeVPN || model.needsVPNRecovery || unavailable)
            .help(unavailable ? model.vpnUnavailableMessage : mode == .internetVPN && nativeVPN.available ? "macOS requests administrator authorization. Meshway verifies owned device capture and restores its changes when you disconnect." : mode.explanation)
    }

    private var serverList: some View {
        Panel {
            VStack(alignment: .leading, spacing: 18) {
                HStack { Text("Your servers").font(.headline); Spacer(); Text("\(model.servers.count) known").font(.caption).foregroundStyle(MeshwayTheme.muted) }
                Text(model.catalogMessage).font(.callout).foregroundStyle(MeshwayTheme.muted)
                if model.status == nil { Button("Start peer node") { Task { await model.start() } }.disabled(model.busy) }
                ForEach(model.connectionMode == .personalTunnel ? model.servers.filter { $0.personal == true || $0.peer_id == model.configuration.sshPeerID } : model.servers) { server in
                    HStack(spacing: 13) {
                        Image(systemName: server.roles.exit ? "server.rack" : "network").font(.title3).foregroundStyle(MeshwayTheme.accent).frame(width: 38, height: 38).background(MeshwayTheme.inset, in: RoundedRectangle(cornerRadius: 10))
                        VStack(alignment: .leading, spacing: 5) {
                            Text(server.label.isEmpty ? "Peer server" : server.label).font(.system(size: 14, weight: .semibold))
                            Text([server.country ?? "", server.roles.exit ? "Internet exit" : server.roles.relay ? "Encrypted relay" : "Private peer", server.available ? (server.can_connect ? "Available" : "Approval needed") : "Unavailable"].filter { !$0.isEmpty }.joined(separator: " · ")).font(.caption).foregroundStyle(MeshwayTheme.muted)
                        }
                        Spacer()
                        if model.selectedPeer == server.peer_id { Text("Selected").font(.caption).foregroundStyle(MeshwayTheme.green) }
                        Button(model.selectedPeer == server.peer_id ? "Reconnect" : "Connect") { Task { await model.selectServer(server) } }
                            .disabled(model.busy || !server.roles.exit || model.activeVPN || model.needsVPNRecovery || (model.deviceVPN && !model.deviceVPNAvailable))
                    }.padding(13).background(MeshwayTheme.inset, in: RoundedRectangle(cornerRadius: 12))
                }
                Text("Only discovered or invited servers appear here. Location labels are supplied by their owners.").font(.caption).foregroundStyle(MeshwayTheme.muted)
            }
        }
    }

    private var invitation: some View {
        Panel {
            VStack(alignment: .leading, spacing: 14) {
                Text("Add a server invitation").font(.headline)
                Text("Paste the connection address from your server or a trusted owner. A private server checks your account before forwarding traffic.").font(.callout).foregroundStyle(MeshwayTheme.muted)
                HStack { TextField("/ip4/…/tcp/…/p2p/…", text: $model.connectAddress); Button("Add server") { Task { await model.connect() } }.disabled(model.status == nil || model.busy || model.connectAddress.isEmpty) }
            }
        }
    }

    private var connections: some View {
        VStack(spacing: 20) {
            Panel {
                VStack(alignment: .leading, spacing: 16) {
                    HStack { Text("Peer node").font(.headline); Spacer(); Button("Stop node") { Task { await model.stop() } }.disabled(!model.runningOwnedProcess || model.busy) }
                    if let status = model.status {
                        detailRow("Peer identity", status.peer_id)
                        detailRow("Selected server", status.upstream_peer.isEmpty ? "None" : status.upstream_peer)
                        detailRow("Server transport", status.upstream_connected == true ? "Connected" : "Not connected")
                        if nativeVPN.state != .disconnected { detailRow("Device VPN", nativeVPN.state.rawValue); if let device = nativeVPN.device { detailRow("Capture interface", device) } }
                        if let packet = status.vpn { detailRow("VPN peer", packet.peer_id ?? "None"); detailRow("Packet transport", packet.active ? "Active" : "Reconnecting / inactive") }
                        detailRow("SOCKS5 listener", status.socks_listen.isEmpty ? "Disabled" : status.socks_listen)
                        if let transport = status.transport {
                            detailRow("Personal transport", transport.kind == "ssh" ? (transport.authenticated ? "SSH · authenticated Meshway peer" : transport.ready ? "SSH ready · waiting for Meshway authentication" : "SSH not ready") : "Direct encrypted peer")
                            if let error = transport.error, !error.isEmpty { detailRow("Transport error", error) }
                        }
                        detailRow("SOCKS payload + VPN packets", "\(bytes(status.bytes_sent)) sent · \(bytes(status.bytes_received)) received")
                        ForEach(status.addresses, id: \.self) { detailRow("Listening address", $0) }
                    } else { Text("The peer node is stopped.").foregroundStyle(MeshwayTheme.muted); Button("Start node") { Task { await model.start() } }.disabled(model.busy) }
                    Text("Disconnect releases the selected exit. Stop node closes this app’s daemon and local listeners.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                }
            }
            Panel {
                VStack(alignment: .leading, spacing: 15) {
                    Text("Live peers").font(.headline)
                    if model.status?.peers.isEmpty != false { Text("No connected peers.").font(.callout).foregroundStyle(MeshwayTheme.muted) }
                    ForEach(model.status?.peers ?? []) { peer in
                        VStack(alignment: .leading, spacing: 6) { Text(peer.id).font(.system(.caption, design: .monospaced)).textSelection(.enabled); ForEach(peer.addresses ?? [], id: \.self) { Text($0).font(.caption2).foregroundStyle(MeshwayTheme.muted).textSelection(.enabled) } }
                    }
                }
            }
        }
    }

    private var accountTools: some View {
        VStack(spacing: 20) {
            Panel {
                VStack(alignment: .leading, spacing: 13) {
                    Text("Verify your exit IP").font(.headline)
                    Text("Make a certificate-verified HTTPS request through your selected SOCKS exit. This checks that route without changing macOS proxy settings; it does not claim every app uses it.").font(.callout).foregroundStyle(MeshwayTheme.muted)
                    if let ip = model.observedExitIP { Text(ip).font(.system(.body, design: .monospaced)).textSelection(.enabled) }
                    HStack {
                        Button("Check selected exit IP") { Task { await model.verifyExitIP() } }.disabled(model.busy || !model.peerConnected)
                        Button("Check browser IP") { model.openBrowserIPCheck() }
                    }
                    Text("Browser check opens a fresh api64.ipify.org URL in your browser. That browser’s own proxy settings and connection pools determine its route; it does not prove every app is captured.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                    Text("Uses the public api.ipify.org endpoint. No location is inferred.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                }
            }
            Panel {
                VStack(alignment: .leading, spacing: 15) {
                    Text("Your private-key account").font(.headline)
                    Text("Your recovery key joins your devices and personal servers. Each device has its own identity. Meshway cannot recover a lost key.").font(.callout).foregroundStyle(MeshwayTheme.muted)
                    HStack { Button("Save recovery key…") { Task { await model.exportRecovery() } }; Button("Import recovery key…") { Task { await model.importRecovery() } } }
                        .disabled(model.status != nil || model.runningOwnedProcess || model.busy || model.activeVPN)
                    Text("Stop the node and device VPN before changing or exporting account keys. Your backup must stay private.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                    DisclosureGroup("Replace a compromised recovery key") {
                        VStack(alignment: .leading, spacing: 12) { Text("Rotation creates a new account ID. Import the new backup on every trusted device and personal server.").font(.caption).foregroundStyle(MeshwayTheme.muted); Button("Replace recovery key…") { Task { await model.rotateRecovery() } }.disabled(model.status != nil || model.runningOwnedProcess || model.busy || model.activeVPN) }.padding(.top, 10)
                    }
                }
            }
            Panel {
                VStack(alignment: .leading, spacing: 15) {
                    HStack { Text("Devices and local history").font(.headline); Spacer(); Button("Refresh") { Task { await model.refreshAccount() } }.disabled(model.status == nil || model.busy) }
                    Text("History stays on this device unless you enable account sync and request it with a trusted peer.").font(.callout).foregroundStyle(MeshwayTheme.muted)
                    DisclosureGroup("View account records") { ScrollView { Text(model.accountText).font(.system(.caption2, design: .monospaced)).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading) }.frame(maxHeight: 280).padding(.top, 10) }
                    HStack { TextField("Trusted peer ID for sync", text: $model.accountSyncPeer); Button("Sync") { Task { await model.syncAccount() } }.disabled(model.status == nil || model.busy || model.accountSyncPeer.isEmpty) }
                    DisclosureGroup("Device revocation and history deletion") {
                        VStack(alignment: .leading, spacing: 12) {
                            HStack { TextField("Device peer ID to revoke", text: $model.revokedPeer); Button("Revoke…") { Task { await model.revokeDevice() } }.disabled(model.status == nil || model.busy || model.revokedPeer.isEmpty) }
                            Toggle("Apply history deletion to synced devices", isOn: $model.syncHistoryDeletion)
                            Button("Delete connection history…") { Task { await model.clearHistory() } }.disabled(model.status == nil || model.busy)
                        }.padding(.top, 10)
                    }
                }
            }
        }
    }

    private var settings: some View {
        VStack(spacing: 20) {
            if model.runningOwnedProcess || model.status != nil || model.activeVPN { notice("Stop your node and device VPN to edit settings. Saved changes apply on the next start.", icon: "info.circle", color: MeshwayTheme.accent) }
            Panel {
                VStack(alignment: .leading, spacing: 15) {
                    Text("Local listeners").font(.headline)
                    TextField("SOCKS listener", text: $model.configuration.socksListen)
                    TextField("API listener", text: $model.configuration.apiListen)
                    TextField("Upstream exit peer ID", text: $model.configuration.upstreamPeer)
                    Text("SOCKS and API listeners accept only local connections. Applications choose whether to use the proxy.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                }.disabled(model.runningOwnedProcess || model.status != nil || model.busy || model.activeVPN)
            }
            Panel {
                VStack(alignment: .leading, spacing: 15) {
                    Text("Personal server transport").font(.headline)
                    Text("Meshway keeps its own encrypted peer protocol inside your private SSH connection. The server checks the same-account device certificate; no third-party tunnel subscription is required.").font(.callout).foregroundStyle(MeshwayTheme.muted)
                    if model.status != nil && model.status?.transport == nil {
                        Text("This bundled core uses your existing SSH connection. Managed transport settings require the updated core; use Start Meshway.command for the current private setup.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                    }
                    Toggle("Manage the private SSH connection", isOn: $model.configuration.personalTransportEnabled)
                    if model.configuration.personalTransportEnabled {
                        TextField("Existing SSH config alias", text: $model.configuration.sshAlias)
                        HStack {
                            TextField("Local port", value: $model.configuration.sshLocalPort, format: .number.grouping(.never))
                            TextField("Remote loopback port", value: $model.configuration.sshRemotePort, format: .number.grouping(.never))
                        }
                        TextField("Pinned Meshway server peer ID", text: $model.configuration.sshPeerID)
                        Text("Uses your existing SSH config/agent and strict known-host checking. No password or SSH key is stored by Meshway. Only local loopback is forwarded to the server’s loopback port.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                    }
                }.disabled(model.runningOwnedProcess || model.status != nil || model.busy || model.activeVPN)
            }
            Panel {
                VStack(alignment: .leading, spacing: 14) {
                    Text("Sharing and server roles").font(.headline)
                    Toggle("Serve encrypted relay traffic", isOn: $model.configuration.relay)
                    Toggle("Use as your private server", isOn: $model.configuration.personalServer)
                        .onChange(of: model.configuration.personalServer) { value in if value { model.configuration.exit = true; model.configuration.advertiseServer = true } }
                    Text("Private servers accept devices enrolled with your recovery key. Save a private backup and import it on your server.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                    Toggle("Serve as an internet exit", isOn: $model.configuration.exit)
                        .onChange(of: model.configuration.exit) { value in if !value { model.configuration.allowPrivateTargets = false } }
                    Text("Exit traffic uses this machine’s public IP and bandwidth. Enable this only when you intend to share internet access.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                    Toggle("Allow private / LAN destinations", isOn: $model.configuration.allowPrivateTargets).disabled(!model.configuration.exit)
                }.disabled(model.runningOwnedProcess || model.status != nil || model.busy || model.activeVPN)
            }
            Panel {
                VStack(alignment: .leading, spacing: 14) {
                    DisclosureGroup("Advanced node configuration", isExpanded: $model.showSettings) {
                        VStack(alignment: .leading, spacing: 12) {
                            HStack { TextField("Daemon executable", text: $model.executablePath); Button("Choose…") { model.selectExecutable() } }
                            TextField("Private state directory", text: $model.configuration.stateDirectory)
                            SecureField("Local API token", text: $model.configuration.apiToken)
                            Text("Bootstrap peer addresses · one per line").font(.caption).foregroundStyle(MeshwayTheme.muted)
                            TextEditor(text: $model.bootstrapText).font(.system(.caption, design: .monospaced)).frame(height: 90)
                            Text("Allowed inbound peer IDs · one per line").font(.caption).foregroundStyle(MeshwayTheme.muted)
                            TextEditor(text: $model.allowedPeersText).font(.system(.caption, design: .monospaced)).frame(height: 75)
                            Text("An empty allowlist denies inbound tunnels and exits. Named service/forward definitions are preserved in the configuration file.").font(.caption).foregroundStyle(MeshwayTheme.muted)
                        }.padding(.top, 12)
                    }
                    HStack { Button("Save settings") { model.saveSettings() }; Spacer(); Button("Open configuration folder") { model.openConfiguration() }.buttonStyle(.link) }
                }.disabled(model.runningOwnedProcess || model.status != nil || model.busy || model.activeVPN)
            }
        }
    }

    private func detailRow(_ title: String, _ value: String) -> some View {
        HStack(alignment: .top, spacing: 18) { Text(title).font(.caption).foregroundStyle(MeshwayTheme.muted).frame(width: 120, alignment: .leading); Text(value).font(.system(.caption, design: .monospaced)).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading) }
    }
    private func notice(_ text: String, icon: String, color: Color) -> some View {
        HStack(alignment: .top, spacing: 10) { Image(systemName: icon).foregroundStyle(color); Text(text).font(.callout).foregroundStyle(MeshwayTheme.muted).fixedSize(horizontal: false, vertical: true); Spacer(minLength: 0) }.padding(16).frame(maxWidth: .infinity, alignment: .leading).background(color.opacity(0.065), in: RoundedRectangle(cornerRadius: 12))
    }
}

private struct MeshwayLogo: View {
    var body: some View {
        Canvas { context, size in
            let scale = min(size.width, size.height) / 48
            context.scaleBy(x: scale, y: scale)
            context.fill(Path(roundedRect: CGRect(x: 0, y: 0, width: 48, height: 48), cornerRadius: 14), with: .color(MeshwayTheme.accent))
            var hex = Path()
            for points in [[CGPoint(x: 24, y: 8), CGPoint(x: 38, y: 16), CGPoint(x: 38, y: 32), CGPoint(x: 24, y: 40), CGPoint(x: 10, y: 32), CGPoint(x: 10, y: 16)], [CGPoint(x: 24, y: 16), CGPoint(x: 17, y: 20), CGPoint(x: 17, y: 28), CGPoint(x: 24, y: 32), CGPoint(x: 31, y: 28), CGPoint(x: 31, y: 20)]] {
                hex.move(to: points[0]); for point in points.dropFirst() { hex.addLine(to: point) }; hex.closeSubpath()
            }
            context.fill(hex, with: .color(Color(red: 33 / 255, green: 24 / 255, blue: 47 / 255)), style: FillStyle(eoFill: true))
            var lines = Path(); lines.move(to: CGPoint(x: 17, y: 20)); lines.addLine(to: CGPoint(x: 24, y: 24)); lines.addLine(to: CGPoint(x: 31, y: 20)); lines.move(to: CGPoint(x: 24, y: 24)); lines.addLine(to: CGPoint(x: 24, y: 32))
            context.stroke(lines, with: .color(MeshwayTheme.accent), lineWidth: 2)
        }
    }
}
