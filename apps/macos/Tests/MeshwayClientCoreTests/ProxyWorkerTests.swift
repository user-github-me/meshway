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
    private func readyWorker(directory: URL, secondReply: String) throws -> (ProxyWorkerClient, Int32) {
        let pidFile = directory.appendingPathComponent("child.pid")
        let script = """
        import json,os,struct,sys,time
        def request():
            head=sys.stdin.buffer.read(4)
            assert len(head)==4
            return json.loads(sys.stdin.buffer.read(struct.unpack('>I',head)[0]))
        ready=request()
        with open(sys.argv[1],'w') as marker: marker.write(str(os.getpid()))
        result=json.dumps({'id':ready['id'],'ok':True,'error':None}).encode()
        sys.stdout.buffer.write(struct.pack('>I',len(result))+result);sys.stdout.buffer.flush()
        blocked=request()
        assert blocked['updates'][0]['keys']['host']=='127.0.0.1'
        \(secondReply)
        """
        let worker = ProxyWorkerClient(executable: python, arguments: ["-c",script,pidFile.path], timeout: 10)
        // Complete an actual framed transaction before starting the deadline
        // assertion. Slow interpreter startup must not stand in for a worker
        // already processing the transaction being tested.
        try worker.apply(updates())
        let pid = try XCTUnwrap(Int32(String(contentsOf: pidFile, encoding: .utf8)))
        XCTAssertEqual(kill(pid, 0), 0, "The framed readiness response must come from the live owned child")
        return (worker, pid)
    }
    private func assertReaped(_ pid: Int32, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertEqual(kill(pid, 0), -1, file: file, line: line)
        XCTAssertEqual(errno, ESRCH, "The owned child must have exited and been reaped", file: file, line: line)
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
        let client = ProxyWorkerClient(executable: python, arguments: ["-c", script], timeout: 10)
        try client.apply(updates()); try client.apply(updates())
    }
    func testPendingAuthorizationTimeoutKillsOnlyOwnedChildAndKeepsRecoveryJournal() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let (worker, pid) = try readyWorker(directory: dir, secondReply: "time.sleep(60)")
        var applyCount = 0
        let controller = SystemProxyController(directory: dir, runner: { args, privileged in
            if args[0] == "-listallnetworkservices" { return "Services\nOwned test service\n" }
            if args[0] == "-getsocksfirewallproxy" { return "Enabled: No\nServer: \nPort: 0\nAuthenticated Proxy Enabled: 0\n" }
            applyCount += 1
            let updates = try JSONDecoder().decode([SystemProxyController.Update].self, from: Data(base64Encoded: args[1])!)
            try worker.apply(updates, operationTimeout: 2); return ""
        })
        let start = Date()
        XCTAssertThrowsError(try controller.enable(host: "127.0.0.1", port: 1080)) { error in
            XCTAssertTrue(error is ProxyWorkerFailure)
            XCTAssertTrue(error.localizedDescription.contains("timed out"))
        }
        XCTAssertLessThan(Date().timeIntervalSince(start), 5)
        XCTAssertEqual(applyCount, 1, "Ambiguous timeout must not automatically launch another authorization")
        XCTAssertTrue(controller.needsRecovery)
        XCTAssertTrue(FileManager.default.fileExists(atPath: dir.appendingPathComponent("system-proxy-restore.json").path))
        assertReaped(pid)
    }
    func testOversizedAndPartialResponsesAreBoundedAndWorkerIsReaped() throws {
        for (reply, expected) in [
            ("sys.stdout.buffer.write(struct.pack('>I',131073));sys.stdout.buffer.flush()", "invalid response"),
            ("sys.stdout.buffer.write(struct.pack('>I',100)+b'partial');sys.stdout.buffer.flush()", "timed out")
        ] {
            let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
            let (worker, pid) = try readyWorker(directory: dir, secondReply: reply + ";time.sleep(60)")
            let start = Date()
            XCTAssertThrowsError(try worker.apply(updates(), operationTimeout: 2)) { error in
                XCTAssertTrue(error is ProxyWorkerFailure)
                XCTAssertTrue(error.localizedDescription.contains(expected))
            }
            XCTAssertLessThan(Date().timeIntervalSince(start), 5)
            assertReaped(pid)
        }
    }
    func testMalformedTransactionReplyCannotBeAccepted() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let reply = "data=b'{invalid';sys.stdout.buffer.write(struct.pack('>I',len(data))+data);sys.stdout.buffer.flush();time.sleep(60)"
        let (worker, pid) = try readyWorker(directory: dir, secondReply: reply)
        XCTAssertThrowsError(try worker.apply(updates(), operationTimeout: 2)) { error in
            XCTAssertTrue(error is ProxyWorkerFailure)
            XCTAssertTrue(error.localizedDescription.contains("ended unexpectedly"))
        }
        assertReaped(pid)
    }
    func testReadOnlySettingsChildIsBoundedAndCapsOutputWhileReading() throws {
        let text = try ProxyWorkerClient.readCommand(executable: python, arguments: ["-c", "print('owned read only')"], timeout: 10)
        XCTAssertEqual(text, "owned read only\n")
        for (script, expected) in [
            ("import time;time.sleep(60)", "timed out"),
            ("import sys;sys.stdout.write('x'*131073);sys.stdout.flush()", "too much data")
        ] {
            let start = Date()
            XCTAssertThrowsError(try ProxyWorkerClient.readCommand(executable: python, arguments: ["-c", script], timeout: 6)) { error in
                XCTAssertTrue(error is ProxyWorkerFailure)
                XCTAssertTrue(error.localizedDescription.contains(expected))
            }
            XCTAssertLessThan(Date().timeIntervalSince(start), 9)
        }
    }
}
