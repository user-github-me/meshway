import AppKit
import SwiftUI
#if SWIFT_PACKAGE
import MeshwayClientCore
#endif

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    weak var model: AppModel?
    func applicationWillTerminate(_ notification: Notification) {
        model?.terminateOwnedProcess()
    }
}

@main
struct MeshwayApplication: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var model = AppModel()

    var body: some Scene {
        WindowGroup("Meshway") {
            ContentView(model: model)
                .onAppear { delegate.model = model }
                .frame(minWidth: 860, minHeight: 700)
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
    @Published var runningOwnedProcess = false
    @Published var executablePath: String
    @Published var connectAddress = ""
    @Published var bootstrapText = ""
    @Published var allowedPeersText = ""
    @Published var accountText = "Start the daemon, then refresh your local account and device list."
    @Published var accountSyncPeer = ""
    @Published var servers: [Server] = []
    @Published var catalogMessage = "Start Meshway to see available servers."
    @Published var deviceVPN = true
    let vpn = AppleVPNController()
    @Published var showSettings = false
    @Published var showPeerDetails = false
    let configURL: URL
    private var process: Process?
    private var logHandle: FileHandle?
    private var monitor: Task<Void, Never>?

    init() {
        let directory = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appendingPathComponent("Meshway")
        configURL = directory.appendingPathComponent("config.json")
        // A secure random failure must produce a visible error, not an insecure fallback token.
        do { configuration = try Configuration.makeDefault(stateDirectory: directory.appendingPathComponent("node").path) }
        catch { fatalError("Secure configuration generation failed: \(error.localizedDescription)") }
        let bundled = Bundle.main.url(forResource: "meshway", withExtension: nil)?.path ?? ""
        executablePath = UserDefaults.standard.string(forKey: "daemonExecutable") ?? bundled
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
            do {
                if FileManager.default.fileExists(atPath: previous.path), !FileManager.default.fileExists(atPath: destination.path) {
                    try FileManager.default.copyItem(at: previous, to: destination)
                }
                configuration.stateDirectory = destination.path
                try configuration.save(to: configURL)
            } catch { self.error = "Could not migrate private state into the VPN app group: \(error.localizedDescription)" }
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
        guard process == nil, status == nil else { throw ClientError.invalidConfiguration("Stop the daemon before exporting or importing your recovery key.") }
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
            var arguments = ["account", command, "--config", config, command == "export" ? "--output" : "--input", file.path]
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
        defer { busy = false }
        do {
            try save()
            let api = try DaemonAPI(configuration: configuration)
            if let existing = try? await api.status() {
                status = existing
                message = "A daemon is already running with this configuration. Meshway is viewing its status."
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
            child.environment = ProcessInfo.processInfo.environment.merging(["MESHWAY_STATE_KEY": try NativeStateKey.loadOrCreate().base64EncodedString()]) { _, value in value }
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
                    if exitCode != 0 { self.error = "Daemon exited with code \(exitCode). Open the configuration folder to inspect daemon.log." }
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
        } catch { self.error = error.localizedDescription }
    }

    func stop() async {
        guard let child = process, !busy else { return }
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
            status = try await DaemonAPI(configuration: configuration).status()
            do {
                servers = try await DaemonAPI(configuration: configuration).servers()
                catalogMessage = servers.isEmpty ? "No servers yet. Add an invitation address from a friend or your server." : "Choose a server to connect."
            } catch { servers = []; catalogMessage = "Server list unavailable: \(error.localizedDescription)" }
            if showError { error = nil }
        } catch {
            status = nil
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
        guard !busy, server.available, server.can_connect else { return }
        busy = true
        defer { busy = false }
        do {
            if deviceVPN {
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
            message = "Selected \(server.label). Applications can use the Meshway SOCKS connection."
            error = nil
            await refresh()
        } catch { self.error = error.localizedDescription }
    }

    func disconnect() async {
        if [.connected, .connecting, .reasserting].contains(vpn.status) {
            vpn.disconnect(); message = "Disconnecting device VPN…"; return
        }
        do { try await DaemonAPI(configuration: configuration).disconnect(); message = "Disconnected from the selected exit."; await refresh() }
        catch { self.error = error.localizedDescription }
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

struct ContentView: View {
    @ObservedObject var model: AppModel
    var body: some View {
        HSplitView {
            VStack(alignment: .leading, spacing: 20) {
                HStack {
                    Image(systemName: "network").font(.largeTitle).foregroundStyle(.teal)
                    VStack(alignment: .leading) {
                        Text("Meshway").font(.largeTitle.bold())
                        Text("Your open peer network").foregroundStyle(.secondary)
                    }
                }
                HStack {
                    Circle().fill(model.status == nil ? Color.gray : Color.green).frame(width: 9, height: 9)
                    Text(model.status == nil ? "Daemon offline" : "Daemon running").font(.headline)
                }
                Text(model.message).font(.callout).foregroundStyle(.secondary)
                if let error = model.error {
                    Text(error).foregroundStyle(.red).font(.callout).textSelection(.enabled)
                }
                HStack {
                    Button("Start") { Task { await model.start() } }.disabled(model.busy || model.runningOwnedProcess || model.status != nil)
                    Button("Stop") { Task { await model.stop() } }.disabled(model.busy || !model.runningOwnedProcess)
                    Button { Task { await model.refresh() } } label: { Image(systemName: "arrow.clockwise") }.disabled(model.busy)
                }
                if model.busy { ProgressView().controlSize(.small) }
                if let status = model.status {
                    DisclosureGroup("Connection details", isExpanded: $model.showPeerDetails) {
                        Text("PEER ID").font(.caption).foregroundStyle(.secondary)
                        Text(status.peer_id).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                        Text("SOCKS5").font(.caption).foregroundStyle(.secondary)
                        Text(status.socks_listen.isEmpty ? "Disabled" : status.socks_listen).textSelection(.enabled)
                        Text("\(status.peers.count) connected peers")
                        Text("Sent \(ByteCountFormatter.string(fromByteCount: Int64(clamping: status.bytes_sent), countStyle: .binary)) · Received \(ByteCountFormatter.string(fromByteCount: Int64(clamping: status.bytes_received), countStyle: .binary))")
                            .font(.caption).foregroundStyle(.secondary)
                        ForEach(status.addresses, id: \.self) { Text($0).font(.system(.caption2, design: .monospaced)).textSelection(.enabled) }
                    }
                }
                Spacer()
                Text("Device VPN protects IPv4, IPv6, and DNS through the signed packet extension. SOCKS mode connects individual applications.")
                    .font(.caption).foregroundStyle(.secondary)
                Button("Open configuration folder") { model.openConfiguration() }.buttonStyle(.link)
            }
            .padding(24).frame(minWidth: 280, idealWidth: 310, maxWidth: 360)
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    GroupBox("Servers") {
                        VStack(alignment: .leading, spacing: 14) {
                            Toggle("Protect this device with VPN", isOn: $model.deviceVPN)
                            VPNStatusView(vpn: model.vpn)
                            Text(model.catalogMessage).font(.callout).foregroundStyle(.secondary)
                            if let status = model.status, !status.upstream_peer.isEmpty {
                                Button("Disconnect") { Task { await model.disconnect() } }.disabled(model.busy)
                                Text(status.upstream_connected == true ? "Selected peer connection is live" : "Selected peer is offline; Meshway retries when traffic is requested.").font(.caption).foregroundStyle(.secondary)
                            }
                            ForEach(model.servers) { server in
                                HStack {
                                    VStack(alignment: .leading, spacing: 4) {
                                        Text(server.label.isEmpty ? "Peer server" : server.label).font(.headline)
                                        Text([server.country ?? "", server.roles.exit ? "Internet exit" : server.roles.relay ? "Encrypted relay" : "Private peer"].filter { !$0.isEmpty }.joined(separator: " · ")).font(.caption).foregroundStyle(.secondary)
                                    }
                                    Spacer()
                                    Button(model.status?.upstream_peer == server.peer_id ? "Selected" : "Connect") { Task { await model.selectServer(server) } }
                                        .disabled(model.busy || !server.available || !server.can_connect || model.status?.upstream_peer == server.peer_id)
                                }
                            }
                        }.padding(8)
                    }
                    GroupBox("Add a server invitation") {
                        VStack(alignment: .leading) {
                            TextField("Paste the invitation address", text: $model.connectAddress)
                            Button("Add server") { Task { await model.connect() } }.disabled(model.status == nil || model.busy || model.connectAddress.isEmpty)
                            Text("Ask the server owner for its connection address. They also need to approve this device before you can use its exit or tunnels.").font(.caption).foregroundStyle(.secondary)
                        }.padding(8)
                    }
                    DisclosureGroup("Advanced node settings", isExpanded: $model.showSettings) {
                        VStack(alignment: .leading, spacing: 14) {
                            Text("Daemon executable").font(.caption)
                            HStack { TextField("Choose the meshway executable", text: $model.executablePath); Button("Choose…") { model.selectExecutable() } }
                            TextField("State directory", text: $model.configuration.stateDirectory)
                            TextField("SOCKS listener", text: $model.configuration.socksListen)
                            TextField("API listener", text: $model.configuration.apiListen)
                            SecureField("Local API token", text: $model.configuration.apiToken)
                            TextField("Upstream exit peer ID (optional)", text: $model.configuration.upstreamPeer)
                            Text("Bootstrap peer multiaddresses · one per line").font(.caption)
                            TextEditor(text: $model.bootstrapText).font(.system(.caption, design: .monospaced)).frame(height: 80).border(.gray.opacity(0.3))
                            Text("Allowed inbound peer IDs · one per line").font(.caption)
                            TextEditor(text: $model.allowedPeersText).font(.system(.caption, design: .monospaced)).frame(height: 70).border(.gray.opacity(0.3))
                            Text("An empty peer allowlist denies inbound tunnels and exit requests. Add trusted peer IDs before sharing access.").font(.caption).foregroundStyle(.secondary)
                            Toggle("Serve encrypted relay traffic", isOn: $model.configuration.relay)
                            Toggle("Use as your private server", isOn: $model.configuration.personalServer)
                                .onChange(of: model.configuration.personalServer) { value in if value { model.configuration.exit = true; model.configuration.advertiseServer = true } }
                            Text("Your private server accepts other devices enrolled with the same recovery key. Save your key on this device, then import it on your own server. Keep it private.").font(.caption).foregroundStyle(.secondary)
                            Toggle("Serve as an internet exit", isOn: $model.configuration.exit)
                                .onChange(of: model.configuration.exit) { value in if !value { model.configuration.allowPrivateTargets = false } }
                            Text("Exit traffic uses this machine's public IP address and bandwidth. Enable only if you intend to forward other peers' internet traffic.").font(.caption).foregroundStyle(.secondary)
                            Toggle("Allow exits to reach private / LAN destinations", isOn: $model.configuration.allowPrivateTargets).disabled(!model.configuration.exit)
                            Button("Save settings") { model.saveSettings() }
                        }.padding(8)
                    }.disabled(model.runningOwnedProcess || model.status != nil || model.busy)
                    GroupBox("Local account and devices") {
                        VStack(alignment: .leading, spacing: 12) {
                            Text("Your recovery key identifies your account. Each device keeps its own peer identity. Account history stays on this device unless you enable and request peer sync.").font(.caption).foregroundStyle(.secondary)
                            Button("Refresh account") { Task { await model.refreshAccount() } }.disabled(model.status == nil || model.busy)
                            Text(model.accountText).font(.system(.caption2, design: .monospaced)).textSelection(.enabled)
                            TextField("Account peer ID for optional sync", text: $model.accountSyncPeer)
                            Button("Sync with peer") { Task { await model.syncAccount() } }.disabled(model.status == nil || model.busy || model.accountSyncPeer.isEmpty)
                            HStack {
                                Button("Save recovery key…") { Task { await model.exportRecovery() } }
                                Button("Import recovery key…") { Task { await model.importRecovery() } }
                            }.disabled(model.status != nil || model.runningOwnedProcess || model.busy)
                            Text("Stop Meshway before saving or importing a recovery key. Anyone with your key can enroll devices in your account; keep the backup private.").font(.caption).foregroundStyle(.secondary)
                        }.padding(8)
                    }
                }.padding(24)
            }.frame(minWidth: 450)
        }
    }
}

struct VPNStatusView: View {
    @ObservedObject var vpn: AppleVPNController
    var body: some View {
        VStack(alignment: .leading) {
            Text(vpn.statusText).font(.callout).foregroundStyle(.secondary)
            if let error = vpn.error { Text(error).foregroundStyle(.red) }
            if [.connected, .connecting, .reasserting].contains(vpn.status) {
                Button("Disconnect device VPN") { vpn.disconnect() }
            }
        }
    }
}
