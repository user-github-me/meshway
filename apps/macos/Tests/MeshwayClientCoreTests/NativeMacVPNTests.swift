import Foundation
import XCTest
@testable import MeshwayClientCore

final class NativeMacVPNTests: XCTestCase {
    @MainActor private final class ObservationGate {
        private var continuation: CheckedContinuation<Void,Never>?
        private var arrival: CheckedContinuation<Void,Never>?
        private var entered = false
        func pause() async {
            await withCheckedContinuation { continuation in
                self.continuation = continuation; entered = true
                arrival?.resume(); arrival = nil
            }
        }
        func waitUntilEntered() async {
            if entered { return }
            await withCheckedContinuation { arrival = $0 }
        }
        func release() { continuation?.resume(); continuation = nil }
    }
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
    @MainActor func testReadinessFromAnotherInstanceIsIgnoredUntilOwnCaptureAcknowledges() async throws {
        let plan = try plan(); let controller = NativeMacVPNController()
        var observations = 0; var running = true
        let hooks = NativeMacVPNController.LifecycleHooks(launch:{ _ in .init(isRunning:{running},end:{running=false}) },observe:{
            observations += 1
            return .init(active:true,ready:true,peerID:plan.peerID,instance:observations == 1 ? String(repeating:"f",count:32) : plan.captureInstance,device:"utun99")
        },cleanup:{ _,_ in "{\"active\":false}" },pause:{try? await Task.sleep(for:.milliseconds(1))},now:{ProcessInfo.processInfo.systemUptime},timeout:1)
        try await controller.beginLifecycle(plan:plan,installed:URL(fileURLWithPath:"/private/var/db/meshway-native/test"),script:"never executed",hooks:hooks)
        XCTAssertGreaterThanOrEqual(observations,2);XCTAssertEqual(controller.state,.connected)
        try await controller.stop();XCTAssertFalse(running)
    }
    @MainActor func testCleanupFailurePreservesRecoveryAndDoesNotEndWorkingLauncher() async throws {
        let plan = try plan(); let controller = NativeMacVPNController();var ended=false
        let hooks = NativeMacVPNController.LifecycleHooks(launch:{ _ in .init(isRunning:{true},end:{ended=true}) },observe:{ .init(active:true,ready:true,peerID:plan.peerID,instance:plan.captureInstance,device:"utun99") },cleanup:{ _,_ in throw ClientError.invalidConfiguration("owned cleanup failed") },pause:{try? await Task.sleep(for:.seconds(10))},now:{ProcessInfo.processInfo.systemUptime},timeout:1)
        try await controller.beginLifecycle(plan:plan,installed:URL(fileURLWithPath:"/private/var/db/meshway-native/test"),script:"never executed",hooks:hooks)
        do {try await controller.stop();XCTFail("Failed cleanup cannot allow daemon shutdown")} catch {}
        XCTAssertEqual(controller.state,.recoveryRequired);XCTAssertFalse(ended)
    }
    @MainActor func testLaunchTimeoutEndsOnlyOwnedLauncherAndRunsInstanceScopedCleanup() async throws {
        let plan = try plan();let controller = NativeMacVPNController();var ended=false;var cleanupInstance:String?;var clock:TimeInterval=0
        let hooks = NativeMacVPNController.LifecycleHooks(launch:{ _ in .init(isRunning:{!ended},end:{ended=true}) },observe:{ .init(active:false,ready:false,peerID:plan.peerID,instance:nil,device:nil) },cleanup:{ plan,_ in cleanupInstance=plan.captureInstance;return "{\"active\":false}" },pause:{clock += 1},now:{clock},timeout:2)
        do {try await controller.beginLifecycle(plan:plan,installed:URL(fileURLWithPath:"/private/var/db/meshway-native/test"),script:"never executed",hooks:hooks);XCTFail("Readiness never arrived")} catch {}
        XCTAssertTrue(ended);XCTAssertEqual(cleanupInstance,plan.captureInstance);XCTAssertEqual(controller.state,.disconnected)
    }
    @MainActor func testDelayedMonitorReplyCannotReactivateCaptureAfterStop() async throws {
        let plan = try plan(); let controller = NativeMacVPNController(); let gate = ObservationGate()
        var observations = 0; var running = true
        let hooks = NativeMacVPNController.LifecycleHooks(launch:{ _ in .init(isRunning:{running},end:{running=false}) },observe:{
            observations += 1
            if observations == 2 { await gate.pause() }
            return .init(active:true,ready:true,peerID:plan.peerID,instance:plan.captureInstance,device:"utun99")
        },cleanup:{ _,_ in "{\"active\":false}" },pause:{await Task.yield()},now:{ProcessInfo.processInfo.systemUptime},timeout:1)
        try await controller.beginLifecycle(plan:plan,installed:URL(fileURLWithPath:"/private/var/db/meshway-native/test"),script:"never executed",hooks:hooks)
        await gate.waitUntilEntered()
        try await controller.stop()
        XCTAssertEqual(controller.state,.disconnected); XCTAssertFalse(running)
        gate.release()
        // Allow the captured, pre-stop reply to return to the actual monitor.
        for _ in 0..<5 { await Task.yield() }
        XCTAssertEqual(controller.state,.disconnected,"An observation begun before cleanup cannot revive its stopped capture")
        XCTAssertFalse(controller.active)
    }
    @MainActor func testLauncherLossDuringReadinessObservationCannotReportConnected() async throws {
        let plan = try plan(); let controller = NativeMacVPNController(); let gate = ObservationGate()
        var running = true
        let hooks = NativeMacVPNController.LifecycleHooks(launch:{ _ in .init(isRunning:{running},end:{running=false}) },observe:{
            await gate.pause()
            return .init(active:true,ready:true,peerID:plan.peerID,instance:plan.captureInstance,device:"utun99")
        },cleanup:{ _,_ in "{\"active\":false}" },pause:{await Task.yield()},now:{ProcessInfo.processInfo.systemUptime},timeout:1)
        let starting = Task {
            try await controller.beginLifecycle(plan:plan,installed:URL(fileURLWithPath:"/private/var/db/meshway-native/test"),script:"never executed",hooks:hooks)
        }
        await gate.waitUntilEntered(); running = false; gate.release()
        do { try await starting.value; XCTFail("A readiness reply cannot establish a capture whose owning launcher has already ended") } catch {}
        XCTAssertEqual(controller.state,.recoveryRequired)
        try await controller.stop(); XCTAssertEqual(controller.state,.disconnected)
    }
    @MainActor func testStopDuringDelayedStartCannotAcceptItsOldReadinessReply() async throws {
        let plan = try plan(); let controller = NativeMacVPNController(); let gate = ObservationGate()
        var running = true; var cleanupCount = 0
        let hooks = NativeMacVPNController.LifecycleHooks(launch:{ _ in .init(isRunning:{running},end:{running=false}) },observe:{
            await gate.pause()
            return .init(active:true,ready:true,peerID:plan.peerID,instance:plan.captureInstance,device:"utun99")
        },cleanup:{ _,_ in cleanupCount += 1; return "{\"active\":false}" },pause:{await Task.yield()},now:{ProcessInfo.processInfo.systemUptime},timeout:1)
        let starting = Task {
            try await controller.beginLifecycle(plan:plan,installed:URL(fileURLWithPath:"/private/var/db/meshway-native/test"),script:"never executed",hooks:hooks)
        }
        await gate.waitUntilEntered(); try await controller.stop(); gate.release()
        do { try await starting.value; XCTFail("Cleanup must invalidate the pending start's readiness") } catch {}
        XCTAssertEqual(controller.state,.disconnected); XCTAssertFalse(running)
        try await controller.stop()
        XCTAssertEqual(cleanupCount,1,"Completed cleanup must release its old lifecycle rather than execute it on a later Stop")
    }
}
