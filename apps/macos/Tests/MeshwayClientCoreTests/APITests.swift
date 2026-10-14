import Foundation
import XCTest
@testable import MeshwayClientCore

private final class MockURLProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: ((URLRequest) throws -> (Int, Data))?
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        do {
            guard let handler = Self.handler else { throw URLError(.unknown) }
            let (status, body) = try handler(request)
            let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: body)
            client?.urlProtocolDidFinishLoading(self)
        } catch { client?.urlProtocol(self, didFailWithError: error) }
    }
    override func stopLoading() { }
}

@MainActor
final class APITests: XCTestCase {
    private func client() throws -> (DaemonAPI, Configuration) {
        let configuration = try Configuration.makeDefault(stateDirectory: "/tmp/meshway-test")
        let sessionConfig = URLSessionConfiguration.ephemeral
        sessionConfig.protocolClasses = [MockURLProtocol.self]
        return (try DaemonAPI(configuration: configuration, session: URLSession(configuration: sessionConfig)), configuration)
    }

    func testStatusRequiresTokenAndDecodesRealContract() async throws {
        let (api, config) = try client()
        MockURLProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/v1/status")
            XCTAssertEqual(request.httpMethod, "GET")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer \(config.apiToken)")
            return (200, Data(#"{"version":"0.1.0","peer_id":"12D3Test","addresses":[],"peers":[],"roles":{"relay":false,"exit":false},"socks_listen":"127.0.0.1:1080","api_listen":"127.0.0.1:7777","upstream_peer":"","started_at":"2026-10-14T00:00:00Z","bytes_sent":7,"bytes_received":9}"#.utf8))
        }
        let status = try await api.status()
        XCTAssertEqual(status.peer_id, "12D3Test")
        XCTAssertEqual(status.bytes_received, 9)
    }

    func testUnauthorizedStatusFailsWithoutConnectedState() async throws {
        let (api, _) = try client()
        MockURLProtocol.handler = { _ in (401, Data("unauthorized".utf8)) }
        do { _ = try await api.status(); XCTFail("401 must fail") }
        catch { XCTAssertEqual(error as? ClientError, .requestFailed(401, "unauthorized")) }
    }

    func testRevocationAndSignedHistoryDeletionUseAuthenticatedMutation() async throws {
        let (api, config) = try client()
        MockURLProtocol.handler = { request in
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer \(config.apiToken)")
            if request.url?.path == "/v1/account/revoke" {
                XCTAssertEqual(request.httpMethod, "POST")
            } else {
                XCTAssertEqual(request.url?.path, "/v1/account/history")
                XCTAssertEqual(request.httpMethod, "DELETE")
            }
            return (401, Data("unauthorized".utf8))
        }
        do { try await api.revokeDevice(peerID: "peer"); XCTFail("Revocation must report a rejected request") }
        catch { XCTAssertEqual(error as? ClientError, .requestFailed(401, "unauthorized")) }
        do { try await api.clearHistory(synchronize: true); XCTFail("Deletion must report a rejected request") }
        catch { XCTAssertEqual(error as? ClientError, .requestFailed(401, "unauthorized")) }
    }

    func testConnectAndShutdownUsePostAndBearer() async throws {
        let (api, config) = try client()
        MockURLProtocol.handler = { request in
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer \(config.apiToken)")
            XCTAssertTrue(["/v1/connect", "/v1/shutdown"].contains(request.url!.path))
            return (200, Data("{}".utf8))
        }
        try await api.connect(address: "/ip4/127.0.0.1/tcp/4001/p2p/peer")
        try await api.shutdown()
    }
}
