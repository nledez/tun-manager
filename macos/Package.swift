// swift-tools-version: 6.2

import PackageDescription

// Warnings are errors here for the same reason golangci-lint gates the Go side:
// a warning nobody has to fix is a warning everybody stops reading.
let strict: [SwiftSetting] = [.treatAllWarnings(as: .error)]

let package = Package(
    name: "TunManager",
    platforms: [.macOS(.v26)],
    products: [
        .executable(name: "tun-manager-menubar", targets: ["TunManagerMenuBar"]),
        .library(name: "TunManagerFeed", targets: ["TunManagerFeed"]),
    ],
    targets: [
        // Everything worth testing. No AppKit, no NSApplication: the split is
        // what makes "the interface is not tested" a fact about the build
        // graph rather than a claim in a README.
        .target(name: "TunManagerFeed", swiftSettings: strict),

        // Four files of AppKit setter calls. Deliberately untested.
        .executableTarget(
            name: "TunManagerMenuBar",
            dependencies: ["TunManagerFeed"],
            swiftSettings: strict
        ),

        .testTarget(
            name: "TunManagerFeedTests",
            dependencies: ["TunManagerFeed"],
            swiftSettings: strict
        ),
    ]
)
