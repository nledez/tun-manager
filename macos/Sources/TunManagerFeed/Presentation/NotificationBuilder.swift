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

extension NotificationBuilder {
    /// A notification somebody asked for, to find out whether notifications
    /// arrive at all.
    ///
    /// It exists because every other notification is silent when it fails:
    /// permission may have been refused, or the bundle may not be registered,
    /// and either way nothing appears and nothing says why. This one is asked
    /// for on purpose, so its absence is an answer.
    ///
    /// Its own identifier, and a fixed one: asking twice replaces the first
    /// rather than stacking, and it can never collide with a tunnel called
    /// "test" - those are keyed under "tunnel.".
    public static func sample() -> NotificationRequest {
        NotificationRequest(
            identifier: "test",
            title: "Tun Manager",
            body: "Notifications are working. This one was asked for from About.")
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
