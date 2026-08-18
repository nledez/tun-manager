import Foundation

/// What the interface is told when anything changes.
///
/// One method: the interface redraws from whatever is current rather than
/// applying a stream of deltas, because a menu bar item has no incremental
/// state worth keeping and reconciling one would be a second source of truth.
@MainActor
public protocol FeedObserver: AnyObject {
    func linkDidChange(state: LinkState, snapshot: Snapshot?, publisherVersion: String?)

    /// A fresh view arrived, with what changed since the last one.
    ///
    /// Separate from `linkDidChange` because it means something different: that
    /// one fires whenever anything about the link moves, including a
    /// disconnection, while this one fires only when the publisher said
    /// something new. Notifying from the first would announce a reconnection as
    /// though tunnels had changed.
    func linkDidPublish(snapshot: Snapshot, diff: SnapshotDiff)

    /// One reading of the watched tunnel's counters. Cumulative, not a rate:
    /// differencing them is the consumer's job, because it has to survive a
    /// reading that never arrived anyway.
    func linkDidSample(_ sample: Sample)
}

extension FeedObserver {
    /// Default: an observer that only draws has nothing to do with a diff.
    public func linkDidPublish(snapshot: Snapshot, diff: SnapshotDiff) {}
    /// Default: nothing is watched unless somebody asked.
    public func linkDidSample(_ sample: Sample) {}
}
