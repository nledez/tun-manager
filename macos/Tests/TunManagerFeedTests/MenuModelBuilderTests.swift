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
    MenuModelBuilder.build(
        state: state, snapshot: view, publisherVersion: "v0.2.0", now: now, locale: english)
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

@Test func aTunnelRowCarriesItsInterfaceEndpointAndHandshakeAge() {
    let model = build(
        .live(sawState: true),
        snapshot(
            TunnelStatus(
                name: "alpha", group: "needed", health: .up, device: "utun7",
                endpoint: "192.0.2.10:51820", checkIP: "10.20.30.1",
                lastHandshake: taken.addingTimeInterval(-13), rxBytes: 184_320, txBytes: 92_160)))

    let details = model.sections[0].rows[0].details
    #expect(details.contains("Interface: utun7"))
    #expect(details.contains("Endpoint: 192.0.2.10:51820"))
    #expect(details.contains("Handshake: 25s ago"))
    #expect(details.contains { $0.contains("180 kB") })
    #expect(details.contains("Checks: 10.20.30.1"))
}

@Test func aTunnelWithNothingButItsNameDoesNotRenderEmptyDetailLines() {
    // Every optional key was omitted by the publisher. A row of blank labels
    // would say less than no labels at all.
    let model = build(
        .live(sawState: true),
        snapshot(TunnelStatus(name: "charlie", group: "extra", health: .down)))

    #expect(model.sections[0].rows[0].details.isEmpty)
}

@Test func aTunnelThatIsDownDoesNotShowCounters() {
    // The counters are always on the wire, zero included. Showing them for a
    // tunnel carrying nothing is noise.
    let model = build(
        .live(sawState: true),
        snapshot(
            TunnelStatus(
                name: "charlie", group: "extra", health: .down, endpoint: "charlie.example:51820")))

    #expect(!model.sections[0].rows[0].details.contains { $0.hasPrefix("Received:") })
}

@Test func tunnelsAreGroupedByGroupAndSortedByNameInsideEachGroup() {
    let model = build(
        .live(sawState: true),
        snapshot(
            TunnelStatus(name: "delta", group: "needed", health: .up),
            TunnelStatus(name: "alpha", group: "needed", health: .up),
            TunnelStatus(name: "bravo", group: "extra", health: .down)))

    #expect(model.sections.map(\.header) == ["extra", "needed"])
    #expect(model.sections[1].rows.map(\.title) == ["alpha", "delta"])
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
