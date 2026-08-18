import Foundation
import Testing

@testable import TunManagerFeed

private func view(_ tunnels: (String, Health)...) -> Snapshot {
    Snapshot(
        context: FeedContext(name: "office"),
        taken: Date(timeIntervalSince1970: 1_786_968_191),
        tunnels: tunnels.map { TunnelStatus(name: $0.0, group: "needed", health: $0.1) })
}

@Test func aTunnelThatWentFromUpToDownIsReportedOnceWithBothHealths() {
    let diff = SnapshotDiff.between(view(("alpha", .up)), and: view(("alpha", .down)))

    #expect(diff.healthChanges.count == 1)
    #expect(diff.healthChanges[0].from == .up)
    #expect(diff.healthChanges[0].to == .down)
}

@Test func aTunnelWhoseHealthDidNotChangeIsNotReported() {
    let diff = SnapshotDiff.between(view(("alpha", .up)), and: view(("alpha", .up)))

    #expect(diff.isEmpty)
}

@Test func aTunnelThatAppearedIsReportedAsAnAppearanceAndNotAsAHealthChange() {
    let diff = SnapshotDiff.between(view(("alpha", .up)), and: view(("alpha", .up), ("bravo", .down)))

    #expect(diff.appeared == ["bravo"])
    #expect(diff.healthChanges.isEmpty)
}

@Test func aTunnelThatDisappearedIsReported() {
    let diff = SnapshotDiff.between(view(("alpha", .up), ("bravo", .up)), and: view(("alpha", .up)))

    #expect(diff.disappeared == ["bravo"])
    #expect(diff.healthChanges.isEmpty)
}

@Test func changesAreReportedInTunnelNameOrder() {
    // A dictionary iterates in whatever order it likes; anything built from a
    // diff should not.
    let before = view(("charlie", .up), ("alpha", .up), ("bravo", .up))
    let after = view(("charlie", .down), ("alpha", .down), ("bravo", .down))

    let diff = SnapshotDiff.between(before, and: after)

    #expect(diff.healthChanges.map(\.tunnel) == ["alpha", "bravo", "charlie"])
}

@Test func theFirstSnapshotReportsEveryTunnelAsAnAppearanceAndNoHealthChanges() {
    // Without a previous view everything looks like a change, and treating it
    // as one is how a program notifies about every tunnel at startup.
    let diff = SnapshotDiff.between(nil, and: view(("bravo", .up), ("alpha", .down)))

    #expect(diff.appeared == ["alpha", "bravo"])
    #expect(diff.healthChanges.isEmpty)
    #expect(diff.disappeared.isEmpty)
}
