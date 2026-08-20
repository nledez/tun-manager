import Foundation
import Testing

@testable import TunManagerFeed

private let taken = Date(timeIntervalSince1970: 1_786_968_191)
private let now = taken.addingTimeInterval(12)
private let english = Locale(identifier: "en_US_POSIX")

private func snapshot(_ tunnels: TunnelStatus..., context: FeedContext = FeedContext(name: "office"))
    -> Snapshot
{
    Snapshot(context: context, taken: taken, tunnels: tunnels)
}

private func build(_ state: LinkState, _ view: Snapshot?) -> MenuModel {
    MenuModelBuilder.build(state: state, snapshot: view, now: now, locale: english)
}

@Test func theMenuNamesTheContextAndOmitsThePartsItWasNotGiven() {
    let full = build(
        .live(sawState: true),
        snapshot(
            TunnelStatus(name: "alpha", group: "needed", health: .up),
            context: FeedContext(name: "office", interface: "en0", address: "198.51.100.42")))
    #expect(full.headline == "office — en0 · 198.51.100.42")

    let bare = build(
        .live(sawState: true),
        snapshot(TunnelStatus(name: "alpha", group: "needed", health: .up)))
    #expect(bare.headline == "office")
}

@Test func tunnelsAreGroupedByGroupAndSortedByNameInsideEachGroup() {
    let model = build(
        .live(sawState: true),
        snapshot(
            TunnelStatus(name: "delta", group: "needed", health: .up),
            TunnelStatus(name: "alpha", group: "needed", health: .up),
            TunnelStatus(name: "bravo", group: "extra", health: .down)))

    #expect(model.sections.map(\.header) == ["needed", "extra"])
    #expect(model.sections[0].rows.map(\.title) == ["alpha", "delta"])
}

@Test func tunnelsWithNoGroupAppearLastAndUnderNoHeader() {
    let model = build(
        .live(sawState: true),
        snapshot(
            TunnelStatus(name: "loose", group: "", health: .up),
            TunnelStatus(name: "alpha", group: "needed", health: .up)))

    #expect(model.sections.map(\.header) == ["needed", nil])
}

@Test func aSnapshotFromBeforeADisconnectionIsLabelledAsLastKnown() {
    // A kept view that read as current would be exactly the failure this
    // application exists to avoid.
    let view = snapshot(TunnelStatus(name: "alpha", group: "needed", health: .up))

    #expect(build(.live(sawState: true), view).footnote == "Updated 12s ago")
    #expect(build(.retrying(because: .lost), view).footnote == "Last known 12s ago")
}

@Test func eachReasonForNotBeingConnectedGetsItsOwnSentence() {
    // Each has a different remedy, so each deserves different words.
    let sentences = [
        Disconnection.notRunning, .refused, .notPermitted, .lost, .goodbye, .failed(42),
    ].map { build(.retrying(because: $0), nil).headline }

    #expect(Set(sentences).count == sentences.count, "two reasons were given the same sentence")
    #expect(sentences[0] == "tun-manager is not running")
    #expect(sentences[2].contains("sudo"))
}

@Test func aBlockedLinkSaysWhichSchemaEachSideSpeaks() {
    let model = build(.blocked(theirSchema: 4), nil)

    #expect(model.headline.contains("4"))
    #expect(model.headline.contains("\(LinkMachine.schema)"))
    #expect(!model.canRefresh)
}

@Test func refreshIsOfferedOnlyWhenThereIsSomethingToAsk() {
    #expect(build(.live(sawState: true), nil).canRefresh)
    #expect(!build(.retrying(because: .notRunning), nil).canRefresh)
    #expect(!build(.idle, nil).canRefresh)
}

@Test func aMenuWithNothingKnownYetSaysSoRatherThanShowingAnEmptyList() {
    let model = build(.connecting, nil)

    #expect(model.sections.isEmpty)
    #expect(model.footnote == "Nothing known yet")
}

@Test func theAlwaysOnTunnelsComeFirst() {
    // The same ranking internal/app gives the table: needed, then extra, then
    // anything else, then whatever belongs to no group. Alphabetical order put
    // `extra` above `needed`, which reads as though the optional tunnels were
    // the important ones.
    let model = build(
        .live(sawState: true),
        snapshot(
            TunnelStatus(name: "loose", group: "", health: .up),
            TunnelStatus(name: "zulu", group: "spare", health: .up),
            TunnelStatus(name: "charlie", group: "extra", health: .up),
            TunnelStatus(name: "alpha", group: "needed", health: .up)))

    #expect(model.sections.map(\.header) == ["needed", "extra", "spare", nil])
}

@Test func groupsThisProgramHasNoOpinionAboutAreOrderedByName() {
    let model = build(
        .live(sawState: true),
        snapshot(
            TunnelStatus(name: "a", group: "zebra", health: .up),
            TunnelStatus(name: "b", group: "aardvark", health: .up)))

    #expect(model.sections.map(\.header) == ["aardvark", "zebra"])
}

@Test func theMenuOffersTheWholeTableOnceThereIsOne() {
    // The tunnels are listed one by one below, and clicking one opens its own
    // pane. Getting to the table meant clicking a tunnel and then going back,
    // which is a detour past something nobody asked to see.
    let model = build(
        .live(sawState: true), snapshot(TunnelStatus(name: "alpha", group: "needed", health: .up)))

    #expect(model.showsOverview)
}

@Test func theMenuOffersNoTableBeforeThereIsAnything() {
    // Disconnected, the window would open on "No tunnels" and say nothing the
    // headline does not already say.
    let model = build(.connecting, nil)

    #expect(model.showsOverview == false)
}
