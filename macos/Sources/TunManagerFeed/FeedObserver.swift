import Foundation

/// What the interface is told when anything changes.
///
/// One method: the interface redraws from whatever is current rather than
/// applying a stream of deltas, because a menu bar item has no incremental
/// state worth keeping and reconciling one would be a second source of truth.
@MainActor
public protocol FeedObserver: AnyObject {
    func linkDidChange(state: LinkState, snapshot: Snapshot?, publisherVersion: String?)
}
