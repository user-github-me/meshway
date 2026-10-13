import Foundation
import XCTest
@testable import MeshwayClientCore

final class CoreContractTests: XCTestCase {
    func testNativeConfigurationStartsActualDaemon() throws {
        guard let executable = ProcessInfo.processInfo.environment["MESHWAY_DAEMON"], FileManager.default.isExecutableFile(atPath: executable) else {
            throw XCTSkip("Set MESHWAY_DAEMON to test native config against the actual Go daemon.")
        }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        var native = try Configuration.makeDefault(stateDirectory: directory.appendingPathComponent("node").path)
        native.personalServer = true
        native.exit = true
        native.advertiseServer = true
        var object = try JSONSerialization.jsonObject(with: native.encoded()) as! [String: Any]
        object["listen_addresses"] = ["/ip4/127.0.0.1/tcp/0"]
        object["api_listen"] = "127.0.0.1:0"
        object["socks_listen"] = ""
        let path = directory.appendingPathComponent("config.json")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try JSONSerialization.data(withJSONObject: object).write(to: path)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
        let child = Process()
        child.executableURL = URL(fileURLWithPath: executable)
        child.arguments = ["daemon", "--config", path.path]
        let output = Pipe()
        child.standardOutput = output
        child.standardError = output
        try child.run()
        defer { if child.isRunning { child.terminate(); child.waitUntilExit() } }
        Thread.sleep(forTimeInterval: 1)
        if !child.isRunning {
            let failure = output.fileHandleForReading.readDataToEndOfFile()
            XCTFail("Actual daemon rejected native configuration: \(String(decoding: failure, as: UTF8.self))")
        }
        XCTAssertTrue(child.isRunning)
    }
}
