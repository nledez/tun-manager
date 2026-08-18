import Foundation
import Testing

@testable import TunManagerFeed

private func view(_ healths: Health...) -> Snapshot {
    Snapshot(
        context: FeedContext(name: "office"),
        taken: Date(timeIntervalSince1970: 1_786_968_191),
        tunnels: healths.enumerated().map {
            TunnelStatus(name: "t\($0.offset)", group: "needed", health: $0.element)
        })
}

@Test func aViewWhereEveryTunnelIsUpShowsTheCheckedShield() {
    let glyph = StatusGlyph.of(state: .live(sawState: true), snapshot: view(.up, .up))

    #expect(glyph.symbol == "checkmark.shield.fill")
    #expect(!glyph.dimmed)
}

@Test func aViewWithOneStaleTunnelAndNoneDownShowsTheWarningShield() {
    let glyph = StatusGlyph.of(state: .live(sawState: true), snapshot: view(.up, .stale))

    #expect(glyph.symbol == "exclamationmark.shield.fill")
}

@Test func aViewWithOneTunnelDownShowsTheCrossedShield() {
    let glyph = StatusGlyph.of(state: .live(sawState: true), snapshot: view(.up, .stale, .down))

    #expect(glyph.symbol == "xmark.shield.fill")
}

@Test func aHealthThisBuildDoesNotKnowIsNotTreatedAsWorking() {
    let glyph = StatusGlyph.of(state: .live(sawState: true), snapshot: view(.up, .unknown("new")))

    #expect(glyph.symbol == "exclamationmark.shield.fill")
}

@Test func aViewWithNoTunnelsConfiguredShowsThePlainShieldNotTheCrossedOne() {
    // Nothing configured is not everything broken.
    let glyph = StatusGlyph.of(state: .live(sawState: true), snapshot: view())

    #expect(glyph.symbol == "shield")
    #expect(!glyph.dimmed)
}

@Test func aLinkThatIsNotRunningDimsTheButtonAndShowsTheSlashedShield() {
    for state in [LinkState.idle, .connecting, .retrying(because: .notRunning)] {
        let glyph = StatusGlyph.of(state: state, snapshot: nil)

        #expect(glyph.symbol == "shield.slash", "\(state)")
        #expect(glyph.dimmed, "\(state)")
    }
}

@Test func aLinkConnectedButWithoutAViewYetSaysSoRatherThanShowingNothing() {
    let glyph = StatusGlyph.of(state: .live(sawState: false), snapshot: nil)

    #expect(glyph.symbol == "shield")
    #expect(glyph.dimmed)
    #expect(glyph.description.contains("waiting"))
}

@Test func everyGlyphCarriesASentenceForVoiceOver() {
    // The item conveys everything through a symbol, so its description is the
    // whole experience for anyone not looking at it.
    let states: [LinkState] = [
        .idle, .connecting, .retrying(because: .lost), .blocked(theirSchema: 4),
        .live(sawState: false), .live(sawState: true),
    ]
    for state in states {
        let glyph = StatusGlyph.of(state: state, snapshot: view(.up))
        #expect(glyph.description.hasPrefix("Tun Manager:"), "\(state)")
        #expect(glyph.description.count > 20, "\(state) has a stub description")
    }
}

// The needed group is the one that has to work; extra is offered rather than
// expected. A crossed shield because an optional tunnel is off is the menu bar
// crying wolf, and the whole value of a status icon is that it is believed.

private func grouped(_ tunnels: (String, String, Health)...) -> Snapshot {
    Snapshot(
        context: FeedContext(name: "office"),
        taken: Date(timeIntervalSince1970: 1_786_968_191),
        tunnels: tunnels.map { TunnelStatus(name: $0.0, group: $0.1, health: $0.2) })
}

private func glyph(_ view: Snapshot) -> StatusGlyph {
    StatusGlyph.of(state: .live(sawState: true), snapshot: view)
}

@Test func everyNeededTunnelUpReadsAsWorkingEvenWithAnExtraDown() {
    let got = glyph(grouped(("alpha", "needed", .up), ("charlie", "extra", .down)))

    #expect(got.symbol.hasPrefix("checkmark.shield"))
    #expect(!got.dimmed)
}

@Test func anOptionalTunnelBeingOffIsStillVisibleWithoutRaisingAnAlarm() {
    // Distinguishable from everything-up, because "one of the optional ones is
    // off" is worth seeing — just not worth a cross.
    let allUp = glyph(grouped(("alpha", "needed", .up), ("charlie", "extra", .up)))
    let extraDown = glyph(grouped(("alpha", "needed", .up), ("charlie", "extra", .down)))

    #expect(allUp.symbol != extraDown.symbol)
}

@Test func aNeededTunnelDownIsTheCrossedShield() {
    let got = glyph(grouped(("alpha", "needed", .down), ("charlie", "extra", .up)))

    #expect(got.symbol == "xmark.shield.fill")
}

@Test func aNeededTunnelStaleIsAWarningRatherThanAFailure() {
    // Up, and nothing getting through. Not the same as down, and not fine.
    let got = glyph(grouped(("alpha", "needed", .stale), ("bravo", "needed", .up)))

    #expect(got.symbol == "exclamationmark.shield.fill")
}

@Test func withNoNeededGroupAtAllEveryTunnelCounts() {
    // A configuration that defines no groups would otherwise satisfy "every
    // needed tunnel is up" by having none, and show all-clear with everything
    // down.
    let got = glyph(grouped(("alpha", "", .down), ("bravo", "", .down)))

    #expect(got.symbol == "xmark.shield.fill")
}

@Test func theDescriptionCountsTheNeededTunnelsSeparately() {
    // For anyone who hears the menu bar rather than looks at it, "2 of 2
    // needed" is the sentence that answers the question.
    let got = glyph(grouped(("alpha", "needed", .up), ("bravo", "needed", .up), ("charlie", "extra", .down)))

    #expect(got.description.contains("2 of 2 needed"))
    #expect(got.description.contains("1 other"))
}
