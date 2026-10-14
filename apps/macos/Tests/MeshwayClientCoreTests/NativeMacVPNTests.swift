import Foundation
import XCTest
@testable import MeshwayClientCore

final class NativeMacVPNTests: XCTestCase {
    private func plan(core: String = "/Applications/Meshway's App.app/Contents/Resources/meshway", config: String = "/Users/test/Meshway config.json") throws -> NativeMacVPNPlan {
        try NativeMacVPNPlan(bundledExecutable:URL(fileURLWithPath:core),configuration:URL(fileURLWithPath:config),peerID:"12D3KooW123456789abcdefghijk",underlayIPs:["203.0.113.7","2001:db8::7"],parentPID:321)
    }
    func testSSHUnderlayUsesResolvedHostAndRejectsOpaqueProxyHops() throws {
        XCTAssertEqual(try NativeMacVPNPlan.sshHost(from:"hostname vps.example\nproxycommand none\nproxyjump none\n"),"vps.example")
        XCTAssertThrowsError(try NativeMacVPNPlan.sshHost(from:"hostname vps.example\nproxycommand nc remote 22\n"))
        XCTAssertThrowsError(try NativeMacVPNPlan.sshHost(from:"hostname vps.example\nproxyjump other\n"))
    }
    func testPlanRejectsNonLiteralAndUnboundedEndpoints() throws {
        XCTAssertThrowsError(try NativeMacVPNPlan(bundledExecutable:URL(fileURLWithPath:"/app/meshway"),configuration:URL(fileURLWithPath:"/config.json"),peerID:"12D3KooW123456789abcdefghijk",underlayIPs:["localhost"],parentPID:321))
        XCTAssertFalse(NativeMacVPNPlan.isLiteralIP("abcd:invalid"))
        XCTAssertFalse(NativeMacVPNPlan.isLiteralIP("fe80::1%en0"))
        XCTAssertTrue(NativeMacVPNPlan.isLiteralIP("2001:db8::7"))
    }
    func testAdminLaunchQuotesPathsAndCarriesNoAccountToken() throws {
        let (script,executable) = try plan().installAndStartScript(sha256:String(repeating:"a",count:64))
        XCTAssertEqual(executable.path,"/private/var/db/meshway-native/meshway-"+String(repeating:"a",count:64))
        XCTAssertTrue(script.contains("codesign --verify --strict"))
        XCTAssertTrue(script.contains("--parent-pid"))
        XCTAssertTrue(script.contains("--underlay-ips"))
        XCTAssertTrue(script.contains("with administrator privileges"))
        XCTAssertFalse(script.contains("api_token")); XCTAssertFalse(script.contains("MESHWAY_STATE_KEY"))
        XCTAssertThrowsError(try plan().command(executable:URL(fileURLWithPath:"/tmp/arbitrary"),action:"start"))
    }
    func testShellQuotingExecutesLiteralUntrustedPathTextWithoutSubstitution() throws {
        let literal = "space ' quote $(printf BAD) `printf BAD`"
        let process = Process(); let output = Pipe()
        process.executableURL = URL(fileURLWithPath:"/bin/sh")
        process.arguments = ["-c","/usr/bin/printf '%s' " + NativeMacVPNPlan.shellQuote(literal)]
        process.standardOutput = output;try process.run()
        let result = output.fileHandleForReading.readDataToEndOfFile();process.waitUntilExit()
        XCTAssertEqual(String(decoding:result,as:UTF8.self),literal)
    }
    func testGeneratedAdministratorScriptCompilesWithoutExecutingIt() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("meshway-admin-syntax-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at:directory,withIntermediateDirectories:true,attributes:[.posixPermissions:0o700])
        defer { try? FileManager.default.removeItem(at:directory) }
        let (script,_) = try plan().installAndStartScript(sha256:String(repeating:"a",count:64))
        _ = try ProxyWorkerClient.readCommand(executable:URL(fileURLWithPath:"/usr/bin/osacompile"),arguments:["-o",directory.appendingPathComponent("compiled.scpt").path,"-"],timeout:5,input:Data(script.utf8))
        XCTAssertTrue(FileManager.default.fileExists(atPath:directory.appendingPathComponent("compiled.scpt").path))
    }
}
