import Foundation
import Testing

@testable import TunManagerFeed

private func change(_ tunnel: String, _ from: Health, _ to: Health) -> SnapshotDiff.HealthChange {
    SnapshotDiff.HealthChange(tunnel: tunnel, from: from, to: to)
}

private func diff(
    appeared: [String] = [], disappeared: [String] = [],
    changes: [SnapshotDiff.HealthChange] = []
) -> SnapshotDiff {
    SnapshotDiff(appeared: appeared, disappeared: disappeared, healthChanges: changes)
}

@Test func aTunnelGoingDownIsWorthTelling() {
    let got = NotificationBuilder.requests(for: diff(changes: [change("alpha", .up, .down)]))

    #expect(got.count == 1)
    #expect(got[0].title == "alpha down")
    #expect(got[0].body == "tunnel went from up to down")
}

@Test func theWordingMatchesWhatTheTerminalSays() {
    // internal/notify on the Go side writes exactly this. Two programs saying
    // the same thing two ways is how somebody starts wondering whether they
    // mean the same thing.
    let got = NotificationBuilder.requests(for: diff(changes: [change("bravo", .down, .up)]))

    #expect(got[0].title == "bravo up")
    #expect(got[0].body == "tunnel went from down to up")
}

@Test func aTunnelThatMerelyAppearedIsNotWorthTelling() {
    // A tunnel appears because a .conf was imported, not because anything
    // happened to the network. The first view after a start reports every
    // tunnel as an appearance, and notifying then would fire a burst at launch.
    let got = NotificationBuilder.requests(for: diff(appeared: ["alpha", "bravo"]))

    #expect(got.isEmpty)
}

@Test func aTunnelThatDisappearedIsNotWorthTellingEither() {
    let got = NotificationBuilder.requests(for: diff(disappeared: ["charlie"]))

    #expect(got.isEmpty)
}

@Test func aHealthThisBuildDoesNotKnowIsStillWorthTelling() {
    // Better an awkward sentence than silence about a tunnel that changed.
    let got = NotificationBuilder.requests(for: diff(changes: [change("alpha", .up, .unknown("odd"))]))

    #expect(got.count == 1)
    #expect(got[0].title.contains("alpha"))
    #expect(got[0].body.contains("odd"))
}

@Test func eachTunnelGetsItsOwnIdentifierSoOneDoesNotReplaceAnother() {
    // Notification centre replaces a notification with the same identifier. Two
    // tunnels changing at once must produce two banners, not one.
    let got = NotificationBuilder.requests(
        for: diff(changes: [change("alpha", .up, .down), change("bravo", .up, .down)]))

    #expect(Set(got.map(\.identifier)).count == 2)
}

@Test func theSameTunnelChangingTwiceReusesItsIdentifier() {
    // The second banner should replace the first rather than stack: what
    // matters is where the tunnel is now.
    let first = NotificationBuilder.requests(for: diff(changes: [change("alpha", .up, .down)]))
    let second = NotificationBuilder.requests(for: diff(changes: [change("alpha", .down, .up)]))

    #expect(first[0].identifier == second[0].identifier)
}

@Test func nothingChangingIsNothingToSay() {
    #expect(NotificationBuilder.requests(for: diff()).isEmpty)
}
