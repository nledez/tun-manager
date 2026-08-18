/// Why the last connection ended, which is what decides how soon to try again
/// and what the menu says in the meantime.
public enum Disconnection: Sendable, Equatable {
    /// connect(2) found nothing there. The normal state: there is no daemon.
    case notRunning
    /// A socket file left behind by a process that was killed outright.
    case refused
    /// The socket is still root's, because tun-manager was started without a
    /// user to hand it to.
    case notPermitted
    /// Accepted and closed without a word — tun-manager is shutting down.
    case rejected
    /// A `bye`: an orderly exit.
    case goodbye
    /// End of stream after a live connection. A crash, or this client was
    /// dropped for falling behind. Indistinguishable from here.
    case lost
    case failed(Int32)
}

/// Where the link is.
public enum LinkState: Sendable, Equatable {
    case idle
    case connecting
    /// `sawState` is false between the publisher's hello and its first view,
    /// which is a real interval: a freshly started publisher has nothing to
    /// send until its first refresh.
    case live(sawState: Bool)
    case retrying(because: Disconnection)
    /// The publisher speaks a schema this build does not. No automatic retry:
    /// it ends when a human upgrades one side.
    case blocked(theirSchema: Int)
}
