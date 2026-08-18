/// Everything that can happen to the link.
public enum LinkEvent: Sendable, Equatable {
    case start
    case stop
    case connectFailed(Int32)
    case message(FeedMessage)
    /// A clean end of stream.
    case endOfStream
    /// read(2) failed.
    case streamFailed
    case retryTimerFired
    case userAskedToRetry
    case systemDidWake
    case menuWillOpen
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
}
