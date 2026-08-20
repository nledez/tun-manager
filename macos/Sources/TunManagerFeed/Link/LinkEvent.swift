/// Everything that can happen to the link.
public enum LinkEvent: Sendable, Equatable {
    case start
    case stop
    case connectFailed(Int32)
    /// The socket was reachable and whoever is on it is not root. A refusal
    /// made by this program rather than by the kernel, which is why it is not a
    /// connectFailed with an errno in it.
    case publisherNotRoot(uid: UInt32?)
    case message(FeedMessage)
    /// A clean end of stream.
    case endOfStream
    /// read(2) failed.
    case streamFailed
    case retryTimerFired
    case userAskedToRetry
    case systemDidWake
    case menuWillOpen
    /// The detail window is looking at this tunnel. Replaces whatever it was
    /// looking at before: there is one window and it shows one tunnel.
    case watch(String)
    /// The window closed.
    case watchNothing
    /// Somebody asked for a probe of this tunnel, or of every tunnel when the
    /// name is nil. Dropped while there is no connection: unlike a watch there
    /// is nothing to restore, because a measurement is wanted now or not at all.
    case askForPing(String?)
    /// The publisher was asked to prove which one it is and has not, within the
    /// time the machine asked to be woken after.
    case authTimedOut
}

/// Everything the machine can ask the world to do. The machine performs no I/O
/// itself, in the shape internal/tui already uses: Update is pure, effects come
/// back as commands.
public enum LinkAction: Sendable, Equatable {
    case connect
    case closeConnection
    case scheduleRetry(Duration)
    case cancelRetry
    case send(ClientCommand)
    case publish(Snapshot, SnapshotDiff)
    case publishSample(Sample)
    /// Wake the machine if the publisher has not answered by then.
    case scheduleAuthTimeout(Duration)
    case cancelAuthTimeout
    /// Remember this key as the one this socket's publisher is known by, from
    /// now on. Trust on first use: there is nothing to configure, and every
    /// connection after this one is compared against it.
    case pin(String)
}
