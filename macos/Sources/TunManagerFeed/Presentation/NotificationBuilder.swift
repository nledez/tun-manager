/// One notification, decided but not yet posted.
///
/// A value rather than a call, so what gets said is testable without a
/// notification centre, a user session, or somebody's permission.
public struct NotificationRequest: Sendable, Equatable {
    /// Notification centre replaces a notification carrying an identifier it
    /// already has. Keying on the tunnel is what makes a second change to the
    /// same tunnel replace the first rather than stack: what matters is where
    /// that tunnel is now.
    public let identifier: String
    public let title: String
    public let body: String
}

/// Turns what changed into what is worth saying.
public enum NotificationBuilder {
    /// Only health changes are reported.
    ///
    /// An appearance means a .conf was imported and a disappearance means one
    /// was removed — neither is news about the network. It also means the first
    /// view after a start says nothing: with no previous view every tunnel
    /// counts as an appearance, and notifying then would fire a burst of
    /// banners at launch.
    ///
    /// The wording is the wording internal/notify uses on the Go side. Two
    /// programs describing the same event in two ways is how somebody starts
    /// wondering whether they mean the same thing.
    public static func requests(for diff: SnapshotDiff) -> [NotificationRequest] {
        diff.healthChanges.map { change in
            NotificationRequest(
                identifier: "tunnel.\(change.tunnel)",
                title: "\(change.tunnel) \(change.to.wireName)",
                body: "tunnel went from \(change.from.wireName) to \(change.to.wireName)")
        }
    }
}

extension Health {
    /// The name the publisher uses, so a notification and the table agree.
    public var wireName: String {
        switch self {
        case .up: "up"
        case .stale: "stale"
        case .down: "down"
        case .unknown(let raw): raw
        }
    }
}
