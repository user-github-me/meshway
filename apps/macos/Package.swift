// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "MeshwayMac",
    platforms: [.macOS(.v13)],
    products: [.executable(name: "Meshway", targets: ["MeshwayMac"])],
    targets: [
        .target(name: "MeshwayClientCore"),
        .executableTarget(name: "MeshwayMac", dependencies: ["MeshwayClientCore"]),
        .testTarget(name: "MeshwayClientCoreTests", dependencies: ["MeshwayClientCore"]),
    ]
)
