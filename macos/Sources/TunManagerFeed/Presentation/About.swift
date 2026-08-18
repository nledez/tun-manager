/// What the About panel says.
///
/// A value rather than a panel, so the wording is decided and tested here and
/// the interface only displays it.
public struct About: Sendable, Equatable {
    public let appVersion: String
    /// The full `git describe` of this build. Equal to the version for a
    /// release, longer for anything built between tags.
    public let build: String
    /// What tun-manager announced in its hello, or nil before one arrived.
    public let publisherVersion: String?
    public let socketPath: String
    public let flavour: Flavour

    public init(
        appVersion: String, build: String, publisherVersion: String?, socketPath: String,
        flavour: Flavour = .release
    ) {
        self.appVersion = appVersion
        self.build = build
        self.publisherVersion = publisherVersion
        self.socketPath = socketPath
        self.flavour = flavour
    }

    /// A development build says so in its title, so a screenshot of the panel
    /// carries the answer to "which one was that".
    public var title: String {
        guard let label = flavour.label else { return "Tun Manager \(appVersion)" }
        return "Tun Manager \(appVersion) — \(label)"
    }

    /// Two programs are involved here and they version separately, so both
    /// answers are given and both are labelled.
    public var details: [String] {
        var lines: [String] = []
        // A release build's describe is the tag itself; printing it as well
        // would say the same thing twice.
        if build != appVersion && build != "v\(appVersion)" {
            lines.append("Build \(build)")
        }
        lines.append("tun-manager \(publisherVersion ?? "not connected")")
        lines.append("Socket \(socketPath)")
        return lines
    }
}
