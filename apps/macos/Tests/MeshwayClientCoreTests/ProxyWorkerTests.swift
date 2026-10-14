import Foundation
import XCTest
@testable import MeshwayClientCore

final class ProxyWorkerTests: XCTestCase {
    private let python = URL(fileURLWithPath: "/usr/bin/python3")
    private func updates() -> [SystemProxyController.Update] {
        [.init(service: "Owned test service", keys: .init(host: "127.0.0.1", port: 1080, enabled: 1), expected: nil)]
    }
    private func directory() throws -> URL {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("meshway-worker-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        return url
    }
    func testPersistentOwnedChildFramesMultipleTransactionsWithoutSecretsInArguments() throws {
        let script = """
        import sys,json,struct
        while True:
            head=sys.stdin.buffer.read(4)
            if len(head)!=4: break
            payload=sys.stdin.buffer.read(struct.unpack('>I',head)[0]); request=json.loads(payload)
            assert request['updates'][0]['keys']['host']=='127.0.0.1'
            result=json.dumps({'id':request['id'],'ok':True,'error':None}).encode()
            sys.stdout.buffer.write(struct.pack('>I',len(result))+result);sys.stdout.buffer.flush()
        """
        let client = ProxyWorkerClient(executable: python, arguments: ["-c", script], timeout: 2)
        try client.apply(updates()); try client.apply(updates())
    }
    func testPendingAuthorizationTimeoutKillsOnlyOwnedChildAndKeepsRecoveryJournal() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let pidFile = dir.appendingPathComponent("child.pid")
        let script = """
        import os,sys,time
        open(sys.argv[1],'w').write(str(os.getpid()))
        sys.stdin.buffer.read(4)
        time.sleep(60)
        """
        let worker = ProxyWorkerClient(executable: python, arguments: ["-c",script,pidFile.path], timeout: 0.2)
        var applyCount = 0
        let controller = SystemProxyController(directory: dir, runner: { args, privileged in
            if args[0] == "-listallnetworkservices" { return "Services\nOwned test service\n" }
            if args[0] == "-getsocksfirewallproxy" { return "Enabled: No\nServer: \nPort: 0\nAuthenticated Proxy Enabled: 0\n" }
            applyCount += 1
            let updates = try JSONDecoder().decode([SystemProxyController.Update].self, from: Data(base64Encoded: args[1])!)
            try worker.apply(updates); return ""
        })
        let start = Date()
        XCTAssertThrowsError(try controller.enable(host: "127.0.0.1", port: 1080))
        XCTAssertLessThan(Date().timeIntervalSince(start), 3)
        XCTAssertEqual(applyCount, 1, "Ambiguous timeout must not automatically launch another authorization")
        XCTAssertTrue(controller.needsRecovery)
        XCTAssertTrue(FileManager.default.fileExists(atPath: dir.appendingPathComponent("system-proxy-restore.json").path))
        let pid = try XCTUnwrap(Int32(String(contentsOf: pidFile, encoding: .utf8)))
        XCTAssertEqual(kill(pid, 0), -1)
    }
    func testOversizedAndPartialResponsesAreBoundedAndWorkerIsReaped() throws {
        for reply in ["sys.stdout.buffer.write(struct.pack('>I',131073));sys.stdout.buffer.flush()", "sys.stdout.buffer.write(struct.pack('>I',100)+b'partial');sys.stdout.buffer.flush()"] {
            let script = "import sys,struct,time;sys.stdin.buffer.read(4);" + reply + ";time.sleep(60)"
            let worker = ProxyWorkerClient(executable: python, arguments: ["-c",script], timeout: 0.2)
            let start = Date(); XCTAssertThrowsError(try worker.apply(updates())); XCTAssertLessThan(Date().timeIntervalSince(start), 3)
        }
    }
    func testMalformedTransactionReplyCannotBeAccepted() throws {
        let script = "import sys,struct;sys.stdin.buffer.read(4);data=b'{invalid';sys.stdout.buffer.write(struct.pack('>I',len(data))+data);sys.stdout.buffer.flush()"
        let worker = ProxyWorkerClient(executable: python, arguments: ["-c",script], timeout: 2)
        XCTAssertThrowsError(try worker.apply(updates()))
    }
    func testReadOnlySettingsChildIsBoundedAndCapsOutputWhileReading() throws {
        let text = try ProxyWorkerClient.readCommand(executable: python, arguments: ["-c", "print('owned read only')"], timeout: 2)
        XCTAssertEqual(text, "owned read only\n")
        for script in ["import time;time.sleep(60)", "import sys;sys.stdout.write('x'*131073);sys.stdout.flush()"] {
            let start = Date()
            XCTAssertThrowsError(try ProxyWorkerClient.readCommand(executable: python, arguments: ["-c", script], timeout: 0.2))
            XCTAssertLessThan(Date().timeIntervalSince(start), 3)
        }
    }
}
