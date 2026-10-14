import Foundation
import Darwin

/// An interrupted privileged call may already have committed. The caller must
/// keep its intent journal and inspect the owned keys before stopping a listener.
struct ProxyWorkerFailure: LocalizedError {
    let errorDescription: String?
}

struct ProxyWorkerRequest: Codable {
    let id: String
    let updates: [SystemProxyController.Update]
}
struct ProxyWorkerResponse: Codable {
    let id: String
    let ok: Bool
    let error: String?
}

/// Only the bundled executable is used in production. No transaction, key or
/// authorization token is put on the command line or in a temporary file.
final class ProxyWorkerClient: @unchecked Sendable {
    static let maximumFrame = 128 * 1024
    private let executable: URL
    private let arguments: [String]
    private let timeout: TimeInterval
    private let mutex = NSLock()
    private var child: Process?
    private var input: FileHandle?
    private var output: FileHandle?

    init(executable: URL = Bundle.main.executableURL!, arguments: [String] = ["--meshway-proxy-worker"], timeout: TimeInterval = 120) {
        self.executable = executable; self.arguments = arguments; self.timeout = timeout
    }
    deinit { stop() }

    /// Bound the fixed read-only networksetup command as well. A stuck settings
    /// read must not prevent the queued journal restoration from returning.
    static func readCommand(executable: URL, arguments: [String], timeout: TimeInterval = 10, input: Data? = nil) throws -> String {
        let process = Process(); let pipe = Pipe()
        process.executableURL = executable; process.arguments = arguments
        process.environment = ["PATH":"/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL":"C"]
        let inputPipe = input == nil ? nil : Pipe()
        if let inputPipe { process.standardInput = inputPipe } else { process.standardInput = FileHandle.nullDevice }
        process.standardOutput = pipe; process.standardError = pipe
        let owner = ProxyWorkerClient(executable: executable, arguments: arguments, timeout: timeout)
        try process.run(); pipe.fileHandleForWriting.closeFile()
        owner.child = process; owner.output = pipe.fileHandleForReading
        defer { owner.stop() }
        let fd = pipe.fileHandleForReading.fileDescriptor; let flags = fcntl(fd, F_GETFL)
        guard flags >= 0, fcntl(fd, F_SETFL, flags | O_NONBLOCK) == 0 else { throw owner.failure("Could not configure the owned proxy settings reader.") }
        let deadline = ProcessInfo.processInfo.systemUptime + timeout
        if let inputPipe, let input {
            inputPipe.fileHandleForReading.closeFile()
            owner.input = inputPipe.fileHandleForWriting
            guard input.count <= maximumFrame else { throw ProxyWorkerFailure(errorDescription:"The owned command input is too large.") }
            let inputFD = inputPipe.fileHandleForWriting.fileDescriptor; let inputFlags = fcntl(inputFD,F_GETFL)
            guard inputFlags >= 0, fcntl(inputFD,F_SETFL,inputFlags | O_NONBLOCK) == 0, fcntl(inputFD,F_SETNOSIGPIPE,1) == 0 else { throw owner.failure("Could not configure the owned command input pipe.") }
            try owner.write(input,to:inputFD,until:deadline);inputPipe.fileHandleForWriting.closeFile();owner.input = nil
        }
        var result = Data(); var buffer = [UInt8](repeating: 0, count: 4096)
        while true {
            try owner.wait(fd, events: Int16(POLLIN), until: deadline)
            let count = Darwin.read(fd, &buffer, buffer.count)
            if count == 0 { break }
            if count > 0 {
                guard result.count + count <= maximumFrame else { throw owner.failure("The proxy settings reader returned too much data.") }
                result.append(contentsOf: buffer.prefix(count))
            } else if errno != EAGAIN && errno != EINTR { throw owner.failure("The owned proxy settings reader stopped unexpectedly.") }
        }
        while process.isRunning {
            guard ProcessInfo.processInfo.systemUptime < deadline else { throw owner.failure("The owned proxy settings reader did not finish. Recovery information remains saved.") }
            usleep(10_000)
        }
        guard process.terminationStatus == 0 else { throw ClientError.invalidConfiguration("Could not read macOS proxy settings.") }
        return String(decoding: result, as: UTF8.self)
    }

    func apply(_ updates: [SystemProxyController.Update], operationTimeout: TimeInterval? = nil) throws {
        mutex.lock(); defer { mutex.unlock() }
        let transactionTimeout = operationTimeout ?? timeout
        guard transactionTimeout.isFinite, transactionTimeout > 0, transactionTimeout <= timeout else {
            throw ClientError.invalidConfiguration("The owned proxy transaction deadline must be positive and bounded by its worker deadline.")
        }
        let request = ProxyWorkerRequest(id: UUID().uuidString, updates: updates)
        let payload = try JSONEncoder().encode(request)
        guard !payload.isEmpty, payload.count <= Self.maximumFrame else { throw ClientError.invalidConfiguration("The proxy transaction is too large.") }
        do {
            try startIfNeeded()
            guard let input, let output else { throw failure("The owned proxy worker did not start.") }
            let deadline = ProcessInfo.processInfo.systemUptime + transactionTimeout
            var count = UInt32(payload.count).bigEndian
            var frame = withUnsafeBytes(of: &count) { Data($0) }; frame.append(payload)
            try write(frame, to: input.fileDescriptor, until: deadline)
            let header = try read(4, from: output.fileDescriptor, until: deadline)
            let size = header.reduce(0) { ($0 << 8) | Int($1) }
            guard (1...Self.maximumFrame).contains(size) else { throw failure("The owned proxy worker returned an invalid response.") }
            let response = try JSONDecoder().decode(ProxyWorkerResponse.self, from: read(size, from: output.fileDescriptor, until: deadline))
            guard response.id == request.id else { throw failure("The owned proxy worker returned a mismatched transaction.") }
            guard response.ok else { throw ClientError.invalidConfiguration(response.error ?? "macOS denied the proxy transaction. Recovery information remains saved.") }
        } catch {
            // A terminated worker cannot be allowed to reply to another request.
            // Do not kill shared Apple helpers; the journal handles ambiguity.
            if error is ProxyWorkerFailure { stop() }
            else if !(error is ClientError) { stop(); throw failure("The owned proxy transaction ended unexpectedly. Recovery information remains saved; retry restoration before stopping Meshway.") }
            throw error
        }
    }

    private func startIfNeeded() throws {
        if let child, child.isRunning, input != nil, output != nil { return }
        guard stop() else { throw failure("The previous owned proxy worker has not stopped. Recovery information remains saved; retry later.") }
        let process = Process(); let request = Pipe(); let reply = Pipe()
        process.executableURL = executable; process.arguments = arguments
        process.environment = ["PATH":"/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL":"C"]
        process.standardInput = request; process.standardOutput = reply; process.standardError = FileHandle.nullDevice
        try process.run()
        request.fileHandleForReading.closeFile(); reply.fileHandleForWriting.closeFile()
        input = request.fileHandleForWriting; output = reply.fileHandleForReading; child = process
        for handle in [input!,output!] {
            let flags = fcntl(handle.fileDescriptor, F_GETFL)
            guard flags >= 0, fcntl(handle.fileDescriptor, F_SETFL, flags | O_NONBLOCK) == 0 else { stop(); throw failure("The proxy worker pipes could not be configured.") }
        }
        // Pipe errors are handled explicitly, rather than terminating the GUI.
        guard fcntl(input!.fileDescriptor, F_SETNOSIGPIPE, 1) == 0 else { stop(); throw failure("The proxy worker pipe could not suppress broken-pipe signals.") }
    }
    @discardableResult private func stop() -> Bool {
        input?.closeFile(); output?.closeFile(); input = nil; output = nil
        guard let process = child else { return true }
        if process.isRunning { process.terminate() }
        let end = ProcessInfo.processInfo.systemUptime + 1
        while process.isRunning && ProcessInfo.processInfo.systemUptime < end { usleep(10_000) }
        if process.isRunning { _ = Darwin.kill(process.processIdentifier, SIGKILL) }
        let killEnd = ProcessInfo.processInfo.systemUptime + 1
        while process.isRunning && ProcessInfo.processInfo.systemUptime < killEnd { usleep(10_000) }
        // Do not wait unboundedly on an OS-owned helper or a stalled child.
        guard !process.isRunning else { return false }
        process.waitUntilExit(); child = nil
        return true
    }
    private func wait(_ descriptor: Int32, events: Int16, until deadline: TimeInterval) throws {
        let remaining = deadline - ProcessInfo.processInfo.systemUptime
        guard remaining > 0 else { throw failure("macOS proxy authorization timed out. Recovery information remains saved; complete restoration before stopping Meshway.") }
        var descriptor = pollfd(fd: descriptor, events: events, revents: 0)
        let result = Darwin.poll(&descriptor, 1, Int32(min(remaining * 1000, 1000).rounded(.up)))
        if result < 0 && errno != EINTR { throw failure("The owned proxy worker pipe failed. Recovery information remains saved.") }
        if result > 0 && descriptor.revents & (Int16(POLLERR) | Int16(POLLNVAL)) != 0 { throw failure("The owned proxy worker stopped unexpectedly. Recovery information remains saved.") }
    }
    private func write(_ data: Data, to fd: Int32, until deadline: TimeInterval) throws {
        var offset = 0
        while offset < data.count {
            try wait(fd, events: Int16(POLLOUT), until: deadline)
            let written = data.withUnsafeBytes { raw in Darwin.write(fd, raw.baseAddress!.advanced(by: offset), data.count-offset) }
            if written > 0 { offset += written }
            else if written == 0 || (errno != EAGAIN && errno != EINTR) { throw failure("The owned proxy worker stopped before reading its transaction. Recovery information remains saved.") }
        }
    }
    private func read(_ count: Int, from fd: Int32, until deadline: TimeInterval) throws -> Data {
        var data = Data(count: count); var offset = 0
        while offset < count {
            try wait(fd, events: Int16(POLLIN), until: deadline)
            let received = data.withUnsafeMutableBytes { raw in Darwin.read(fd, raw.baseAddress!.advanced(by: offset), count-offset) }
            if received > 0 { offset += received }
            else if received == 0 || (errno != EAGAIN && errno != EINTR) { throw failure("The owned proxy worker stopped before confirming its transaction. Recovery information remains saved.") }
        }
        return data
    }
    private func failure(_ message: String) -> ProxyWorkerFailure { ProxyWorkerFailure(errorDescription: message) }
}

/// Called before constructing AppModel or accessing the account Keychain.
/// This persistent, ordinary user process retains one AuthorizationRef for all
/// services and subsequent transactions. It has no daemon or network role.
public enum ProxyWorkerServer {
    public static func runIfRequested() -> Bool {
        guard CommandLine.arguments.dropFirst().first == "--meshway-proxy-worker" else { return false }
        guard CommandLine.arguments.count == 2 else { return true }
        let parent = getppid()
        guard parent > 1 else { return true }
        // A worker waiting inside macOS must not survive its owning GUI and
        // later commit an authorization after that app has disappeared.
        let parentExit = DispatchSource.makeProcessSource(identifier: parent, eventMask: .exit, queue: .global())
        parentExit.setEventHandler { Darwin._exit(0) }; parentExit.resume()
        defer { parentExit.cancel() }
        let preferences = AuthorizedProxyPreferences()
        let input = FileHandle.standardInput; let output = FileHandle.standardOutput
        while let payload = readFrame(input) {
            guard let request = try? JSONDecoder().decode(ProxyWorkerRequest.self, from: payload),
                  UUID(uuidString: request.id) != nil, !request.updates.isEmpty, request.updates.count <= 128 else { return true }
            let response: ProxyWorkerResponse
            do { try preferences.apply(request.updates); response = ProxyWorkerResponse(id: request.id, ok: true, error: nil) }
            catch { response = ProxyWorkerResponse(id: request.id, ok: false, error: error.localizedDescription) }
            guard let data = try? JSONEncoder().encode(response), data.count <= ProxyWorkerClient.maximumFrame else { return true }
            var length = UInt32(data.count).bigEndian
            var frame = withUnsafeBytes(of: &length) { Data($0) }; frame.append(data)
            do { try output.write(contentsOf: frame) } catch { return true }
        }
        return true
    }
    private static func readFrame(_ file: FileHandle) -> Data? {
        func read(_ count: Int) -> Data? {
            var result = Data()
            while result.count < count {
                guard let data = try? file.read(upToCount: count-result.count), !data.isEmpty else { return nil }
                result.append(data)
            }
            return result
        }
        guard let prefix = read(4) else { return nil }
        let count = prefix.reduce(0) { ($0 << 8) | Int($1) }
        guard (1...ProxyWorkerClient.maximumFrame).contains(count) else { return nil }
        return read(count)
    }
}
