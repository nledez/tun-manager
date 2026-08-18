import Foundation
import Testing

@testable import TunManagerFeed

private let taken = Date(timeIntervalSince1970: 1_786_968_191)
private let now = taken.addingTimeInterval(12)
private let english = Locale(identifier: "en_US_POSIX")

private func detail(_ tunnel: TunnelStatus) -> TunnelDetail {
    TunnelDetail(tunnel, now: now, locale: english)
}

@Test func aDetailNamesTheTunnelItsHealthAndItsGroup() {
    let got = detail(TunnelStatus(name: "alpha", group: "needed", health: .up))

    #expect(got.name == "alpha")
    #expect(got.health == .up)
    #expect(got.group == "needed")
}

@Test func aTunnelInNoGroupSaysSoRatherThanShowingABlank() {
    let got = detail(TunnelStatus(name: "loose", group: "", health: .up))

    #expect(got.group == "no group")
}

@Test func everyFactTheWireGaveBecomesALabelledRow() {
    let got = detail(
        TunnelStatus(
            name: "alpha", group: "needed", health: .up, device: "utun7",
            endpoint: "192.0.2.10:51820", checkIP: "10.20.30.1",
            lastHandshake: taken.addingTimeInterval(-13), rxBytes: 184_320, txBytes: 92_160))

    let labels = got.facts.map(\.label)
    #expect(labels == ["Interface", "Endpoint", "Handshake", "Received", "Sent", "Checks"])
    #expect(got.facts.first { $0.label == "Handshake" }?.value == "25s ago")
    #expect(got.facts.first { $0.label == "Received" }?.value == "180 kB")
}

@Test func aFactTheWireDidNotGiveIsLeftOutRatherThanShownEmpty() {
    // A row reading "Endpoint —" says less than no row: the publisher omits
    // what it does not know, and so does this.
    let got = detail(TunnelStatus(name: "charlie", group: "extra", health: .down))

    #expect(got.facts.map(\.label) == [])
}

@Test func aTunnelThatIsDownShowsNoCounters() {
    // They are always on the wire, zero included. Showing them for a tunnel
    // carrying nothing is noise.
    let got = detail(
        TunnelStatus(
            name: "charlie", group: "extra", health: .down,
            endpoint: "charlie.example:51820", rxBytes: 0, txBytes: 0))

    #expect(got.facts.map(\.label) == ["Endpoint"])
}

@Test func aHandshakeThatNeverHappenedIsNotAnAge() {
    let got = detail(
        TunnelStatus(name: "bravo", group: "needed", health: .stale, device: "utun8"))

    #expect(!got.facts.contains { $0.label == "Handshake" })
}
