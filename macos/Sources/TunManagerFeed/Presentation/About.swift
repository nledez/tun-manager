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
    /// The public half of the key tun-manager announced, base64, or nil when it
    /// announced none. Never shown as itself: what goes on screen is its
    /// fingerprint, which is what `sudo tun-manager feed-key` prints.
    public let publisherKey: String?
    public let socketPath: String
    public let flavour: Flavour

    public init(
        appVersion: String, build: String, publisherVersion: String?,
        publisherKey: String? = nil, socketPath: String, flavour: Flavour = .release
    ) {
        self.appVersion = appVersion
        self.build = build
        self.publisherVersion = publisherVersion
        self.publisherKey = publisherKey
        self.socketPath = socketPath
        self.flavour = flavour
    }

    /// The publisher's key as somebody can check it: the same fingerprint
    /// `sudo tun-manager feed-key` prints, so the two can be read side by side.
    ///
    /// Three different answers, because they mean three different things. A
    /// publisher that announced no key is one that cannot be told from any
    /// other program listening on that socket. A key that does not decode is a
    /// publisher saying something this application cannot use. And no
    /// connection at all is neither.
    public var feedKey: String {
        guard publisherVersion != nil else { return "not connected" }
        guard let publisherKey else { return "none — tun-manager announced no key" }
        guard let fingerprint = Fingerprint.of(base64: publisherKey) else {
            return "unreadable — what tun-manager announced is not a key"
        }
        return fingerprint
    }

    /// A development build says so in its title, so a screenshot of the panel
    /// carries the answer to "which one was that".
    public var title: String {
        guard let label = flavour.label else { return "Tun Manager \(appVersion)" }
        return "Tun Manager \(appVersion) — \(label)"
    }

    /// The panel's other button. It posts a notification, which is the only way
    /// to find out whether they arrive: every notification this application
    /// sends on its own is silent when it fails - permission refused, bundle
    /// not registered - so one that was asked for is the only one whose absence
    /// means something.
    public var testNotification: String { "Test Notification" }

    /// What to say when the system has been told not to show them. Posting
    /// would do nothing at all, and "nothing happened" is the one answer this
    /// button exists to avoid.
    public var notificationsRefused: String {
        "Tun Manager is not allowed to post notifications. Turn them on in "
            + "System Settings → Notifications → Tun Manager."
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
        lines.append("Feed key \(feedKey)")
        return lines
    }
}
