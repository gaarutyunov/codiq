// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "greeter",
    targets: [
        .target(name: "Greeter"),
        .executableTarget(name: "App", dependencies: ["Greeter"]),
    ]
)
