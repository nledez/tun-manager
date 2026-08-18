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
