import Foundation
import Testing

@testable import TunManagerFeed

private let escape = "office\u{1b}[2J\u{1b}[Hgone"
private let reordered = "moc.elpmaxe\u{202e}:51820"

@Test func whatWouldDriveATerminalOrAMenuIsRemoved() {
    // AppKit draws a newline inside a menu item without complaint, and the same
    // strings are written to the log and read in a terminal.
    let cleaned = Displayable.of("alpha\u{1b}[2Jbravo\nnext\u{7}")

    #expect(cleaned == "alpha[2Jbravonext")
    for bad in ["\u{1b}", "\n", "\u{7}"] {
        #expect(cleaned.contains(bad) == false)
    }
}

@Test func whatWouldReorderTheTextIsRemoved() {
    // U+202E draws "moc.elpmaxe" as "example.com", and most of what this shows
    // is somewhere traffic goes.
    let cleaned = Displayable.of(reordered)

    #expect(cleaned.unicodeScalars.contains { $0.value == 0x202E } == false)
    #expect(cleaned.hasPrefix("moc.elpmaxe"))
}

@Test func whatPeopleActuallyWriteIsLeftAlone() {
    // Accents, other scripts and emoji are not attacks, and a cleaner that eats
    // them is one somebody works around.
    for text in ["café-vpn", "берлин", "東京", "office 🏢", "a_b-c1"] {
        #expect(Displayable.of(text) == text)
    }
    #expect(Displayable.of("a\tb") == "a b")
}

@Test func aValueCannotOwnTheWholeMenu() {
    let long = String(repeating: "é", count: 10_000)

    let cleaned = Displayable.of(long)

    #expect(cleaned.count == Displayable.limit)
    #expect(cleaned.hasSuffix("…"))
}

@Test func theMenuDrawsTheCleanedNameAndAsksAboutTheRealOne() {
    // The one distinction that matters: a name cleaned for display is a
    // different name, and asking the publisher about it would be asking about a
    // tunnel it has never heard of.
    let snapshot = Snapshot(
        context: FeedContext(name: escape), taken: Date(timeIntervalSince1970: 0),
        tunnels: [TunnelStatus(name: "alpha\nbravo", group: "needed", health: .up)])

    let model = MenuModelBuilder.build(state: .live(sawState: true), snapshot: snapshot, now: Date())

    let row = try! #require(model.sections.first?.rows.first)
    #expect(row.name == "alpha\nbravo")
    #expect(row.title == "alphabravo")
    #expect(model.headline.contains("\u{1b}") == false)
    #expect(model.headline.contains("office"))
}

@Test func theTableAndTheWindowCleanEverythingTheyWereSent() {
    let tunnel = TunnelStatus(
        name: "alpha\u{1b}[2J", group: "nee\u{202e}ded", health: .up, device: "utun7\n",
        endpoint: reordered, checkIP: "10.0.0.1\u{1b}", lastHandshake: nil,
        rxBytes: 1, txBytes: 1)
    let ping = Ping(tunnel: tunnel.name, rtt: nil, error: "no route\u{1b}[2J")

    let row = try! #require(
        TunnelTable.rows([tunnel], pings: [tunnel.name: ping], now: Date()).first)
    let detail = TunnelDetail(tunnel, ping: ping, now: Date())

    #expect(row.name == tunnel.name)
    #expect(row.title == "alpha[2J")
    #expect(row.group.unicodeScalars.contains { $0.value == 0x202E } == false)
    #expect(row.endpoint.unicodeScalars.contains { $0.value == 0x202E } == false)
    if case .failed(let reason) = row.ping {
        #expect(reason.contains("\u{1b}") == false)
    } else {
        Issue.record("ping = \(row.ping), want a failure")
    }

    let shown = detail.name + detail.group + detail.facts.map(\.value).joined()
    #expect(shown.contains("\u{1b}") == false)
    #expect(shown.unicodeScalars.contains { $0.value == 0x202E } == false)
}
