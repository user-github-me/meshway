import SwiftUI
import UniformTypeIdentifiers
import Meshway

@MainActor
final class IOSModel: ObservableObject {
    @Published var servers: [[String: Any]] = []
    @Published var message = "Add an invitation from a server owner or your personal server."
    @Published var invitation = ""
    @Published var account = ""
    @Published var busy = false
    @Published var importRecovery = false
    @Published var exportRecovery = false
    @Published var confirmImport = false
    private var pendingRecovery: URL?
    @Published var recoveryDocument = RecoveryDocument(data: Data())
    let vpn = AppleVPNController()
    private var node: MeshwayNode?
    private let work = DispatchQueue(label: "network.meshway.ioscore", qos: .userInitiated)
    private let configURL: URL
    private let stateURL: URL

    init() {
        let root = AppleVPNController.sharedContainer ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        stateURL = root.appendingPathComponent("node")
        configURL = root.appendingPathComponent("client.json")
        Task { await vpn.refresh(); await start() }
    }

    func start() async {
        guard node == nil, ![.connected, .connecting, .reasserting].contains(vpn.status) else { return }
        busy = true; defer { busy = false }
        do {
            let state = stateURL.path, path = configURL
            let result = try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<(MeshwayNode, Data), Error>) in
                work.async {
                    do {
                        var error: NSError?
                        let data: Data
                        if FileManager.default.fileExists(atPath: path.path) { data = try Data(contentsOf: path) }
                        else {
                            let text = MeshwayDefaultConfig(state, &error)
                            if let error { throw error }
                            var object = try JSONSerialization.jsonObject(with: Data(text.utf8)) as! [String: Any]
                            object["api_listen"] = ""; object["socks_listen"] = ""
                            object["listen_addresses"] = ["/ip4/0.0.0.0/tcp/0", "/ip4/0.0.0.0/udp/0/quic-v1"]
                            data = try JSONSerialization.data(withJSONObject: object)
                            try data.write(to: path, options: .atomic)
                            try FileManager.default.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: path.path)
                        }
                        guard let node = MeshwayStartSecure(String(decoding: data, as: UTF8.self), try NativeStateKey.loadOrCreate(), &error) else { throw error ?? NSError(domain: "Meshway", code: 1) }
                        continuation.resume(returning: (node, data))
                    } catch { continuation.resume(throwing: error) }
                }
            }
            node = result.0
            await refresh()
        } catch { message = error.localizedDescription }
    }

    func refresh() async {
        guard let node else { return }
        do {
            var error: NSError?
            let text = node.servers(&error)
            if let error { throw error }
            let data = try JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: Any]
            servers = data?["servers"] as? [[String: Any]] ?? []
            account = node.accountStatus(&error)
            if let error { throw error }
            message = servers.isEmpty ? "No servers yet. Paste an invitation to add one." : "Choose a server to protect this device."
        } catch { message = error.localizedDescription }
    }

    func addServer() async {
        guard let node else { await start(); return }
        busy = true; defer { busy = false }
        do {
            try node.connect(invitation.trimmingCharacters(in: .whitespacesAndNewlines))
            var object = try JSONSerialization.jsonObject(with: Data(contentsOf: configURL)) as! [String: Any]
            var bootstraps = object["bootstrap_peers"] as? [String] ?? []
            if !bootstraps.contains(invitation) { bootstraps.append(invitation) }
            object["bootstrap_peers"] = bootstraps
            try JSONSerialization.data(withJSONObject: object).write(to: configURL, options: .atomic)
            await refresh()
        } catch { message = error.localizedDescription }
    }

    func connect(_ server: [String: Any]) async {
        guard let peer = server["peer_id"] as? String, let address = server["address"] as? String else { return }
        busy = true; defer { busy = false }
        do {
            try node?.stop(); node = nil
            try await vpn.connect(config: Data(contentsOf: configURL), peerID: peer, invitation: address)
            message = "Connecting encrypted device VPN…"
        } catch { message = error.localizedDescription; await start() }
    }

    func disconnect() async { vpn.disconnect(); message = "Disconnecting…" }

    func prepareRecovery() {
        do {
            try node?.stop(); node = nil
            let temporary = configURL.deletingLastPathComponent().appendingPathComponent("recovery-export.json")
            defer { try? FileManager.default.removeItem(at: temporary) }
            var error: NSError?
            guard MeshwayExportRecoverySecure(stateURL.path, temporary.path, try NativeStateKey.loadOrCreate(), &error) else { throw error ?? NSError(domain: "Meshway", code: 2) }
            recoveryDocument = RecoveryDocument(data: try Data(contentsOf: temporary))
            exportRecovery = true
        } catch { message = error.localizedDescription }
    }

    func imported(_ result: Result<[URL], Error>) {
        do {
            let urls = try result.get()
            guard let url = urls.first else { return }
            pendingRecovery = url
            confirmImport = true
        } catch { message = error.localizedDescription }
    }

    func performImport() {
        guard let url = pendingRecovery else { return }
        pendingRecovery = nil
        do {
            let access = url.startAccessingSecurityScopedResource()
            defer { if access { url.stopAccessingSecurityScopedResource() } }
            try node?.stop(); node = nil
            var error: NSError?
            // The preceding confirmation explicitly authorizes replacing the
            // local account and its records; peer identity remains separate.
            guard MeshwayImportRecoverySecure(stateURL.path, url.path, true, try NativeStateKey.loadOrCreate(), &error) else { throw error ?? NSError(domain: "Meshway", code: 3) }
            message = "Account imported on this device."
            Task { await start() }
        } catch { message = error.localizedDescription; Task { await start() } }
    }
}

struct RecoveryDocument: FileDocument {
    static var readableContentTypes: [UTType] { [.json] }
    var data: Data
    init(data: Data) { self.data = data }
    init(configuration: ReadConfiguration) throws { data = configuration.file.regularFileContents ?? Data() }
    func fileWrapper(configuration: WriteConfiguration) throws -> FileWrapper { FileWrapper(regularFileWithContents: data) }
}

@main
struct MeshwayIOSApp: App {
    @StateObject private var model = IOSModel()
    var body: some Scene {
        WindowGroup {
            NavigationStack {
                List {
                    Section {
                        IOSVPNStatus(vpn: model.vpn)
                        Text(model.message).foregroundStyle(.secondary)
                        Button("Disconnect VPN") { Task { await model.disconnect() } }
                        Button("Refresh servers") { Task { await model.start(); await model.refresh() } }
                    }
                    Section("Servers") {
                        ForEach(Array(model.servers.enumerated()), id: \.offset) { _, server in
                            Button {
                                Task { await model.connect(server) }
                            } label: {
                                HStack {
                                    VStack(alignment: .leading) {
                                        Text(server["label"] as? String ?? "Peer server")
                                        if let country = server["country"] as? String { Text(country).font(.caption).foregroundStyle(.secondary) }
                                    }
                                    Spacer(); Text("Connect")
                                }
                            }.disabled(model.busy || server["can_connect"] as? Bool != true || server["available"] as? Bool != true)
                        }
                    }
                    Section("Add your own server") {
                        TextField("Paste invitation", text: $model.invitation).textInputAutocapitalization(.never).autocorrectionDisabled()
                        Button("Add server") { Task { await model.addServer() } }.disabled(model.busy || model.invitation.isEmpty)
                    }
                    Section("Account") {
                        Text("Keep your recovery file private. It lets your other devices join this account.").font(.caption)
                        Button("Save recovery file") { model.prepareRecovery() }
                        Button("Import recovery file") { model.importRecovery = true }
                        DisclosureGroup("Advanced account details") { Text(model.account).font(.caption).textSelection(.enabled) }
                    }
                }.navigationTitle("Meshway")
            }
            .confirmationDialog("Replace this device's account? Save the old recovery file first if you need it. Import clears local device/history records.", isPresented: $model.confirmImport, titleVisibility: .visible) {
                Button("Import and replace local account", role: .destructive) { model.performImport() }
                Button("Cancel", role: .cancel) { }
            }
            .fileImporter(isPresented: $model.importRecovery, allowedContentTypes: [.json], allowsMultipleSelection: false, onCompletion: model.imported)
            .fileExporter(isPresented: $model.exportRecovery, document: model.recoveryDocument, contentType: .json, defaultFilename: "meshway-recovery") { result in
                model.recoveryDocument = RecoveryDocument(data: Data())
                if case .failure(let error) = result { model.message = error.localizedDescription }
                Task { await model.start() }
            }
        }
    }
}

struct IOSVPNStatus: View {
    @ObservedObject var vpn: AppleVPNController
    var body: some View { Text(vpn.statusText).font(.headline) }
}
