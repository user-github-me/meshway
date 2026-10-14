import Foundation
import SystemConfiguration
let path = FileManager.default.temporaryDirectory.appendingPathComponent("meshway-isolated-scpreferences-\(UUID().uuidString).plist")
defer { try? FileManager.default.removeItem(at: path) }
guard let prefs = SCPreferencesCreate(nil, "Meshway isolated proxy lab" as CFString, path.path as CFString),
      let interfaces = SCNetworkInterfaceCopyAll() as? [SCNetworkInterface],
      let interface = interfaces.first(where: { (SCNetworkInterfaceGetBSDName($0) as String?) == "en0" }),
      let service = SCNetworkServiceCreate(prefs, interface),
      SCNetworkServiceSetName(service, "Meshway isolated proxy fixture" as CFString),
      SCNetworkServiceEstablishDefaultConfiguration(service),
      let proxies = SCNetworkServiceCopyProtocol(service, kSCNetworkProtocolTypeProxies) else { fatalError("Could not create isolated SCPreferences service") }
let original: [String: Any] = [kSCPropNetProxiesHTTPEnable as String: 1, kSCPropNetProxiesHTTPProxy as String: "127.0.0.1", kSCPropNetProxiesHTTPPort as String: 7890, kSCPropNetProxiesHTTPSEnable as String: 1, "HTTPUser": "untouched-fixture-metadata", "ExceptionsList": ["localhost"]]
guard SCNetworkProtocolSetConfiguration(proxies, original as CFDictionary) else { fatalError("Isolated snapshot failed") }
var enabled = original
enabled[kSCPropNetProxiesSOCKSProxy as String] = "127.0.0.1"; enabled[kSCPropNetProxiesSOCKSPort as String] = 1080; enabled[kSCPropNetProxiesSOCKSEnable as String] = 1
for key in [kSCPropNetProxiesHTTPEnable,kSCPropNetProxiesHTTPSEnable,kSCPropNetProxiesProxyAutoConfigEnable,kSCPropNetProxiesProxyAutoDiscoveryEnable] { enabled[key as String] = 0 }
guard SCNetworkProtocolSetConfiguration(proxies, enabled as CFDictionary),
      let current = SCNetworkProtocolGetConfiguration(proxies) as? [String: Any], NSDictionary(dictionary: current).isEqual(to: enabled) else { fatalError("Isolated proxy activation mismatch") }
guard SCNetworkProtocolSetConfiguration(proxies, original as CFDictionary),
      let restored = SCNetworkProtocolGetConfiguration(proxies) as? [String: Any], NSDictionary(dictionary: restored).isEqual(to: original) else { fatalError("Isolated restore mismatch") }
print("PASS isolated in-memory SCPreferences proxy dictionary activation/restore; no commit/apply or default system preferences used")
