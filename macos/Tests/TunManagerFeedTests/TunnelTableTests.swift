import Foundation
import Testing

@testable import TunManagerFeed

private let now = Date(timeIntervalSince1970: 1_775_000_000)
private let english = Locale(identifier: "en_US")

private func up(
    _ name: String, group: String = "needed", endpoint: String? = "192.0.2.10:51820",
    handshake: TimeInterval? = 12, rx: Int64 = 184_320, tx: Int64 = 92_160
) -> TunnelStatus {
    TunnelStatus(
        name: name, group: group, health: .up, device: "utun7", endpoint: endpoint,
        checkIP: "10.20.30.1",
        lastHandshake: handshake.map { now.addingTimeInterval(-$0) },
        rxBytes: rx, txBytes: tx)
}

@Test func aRowCarriesTheFourColumnsTheTerminalShows() {
    let rows = TunnelTable.rows([up("alpha")], now: now, locale: english)

    #expect(rows.count == 1)
    #expect(rows[0].handshake == "12s")
    #expect(rows[0].traffic == "180 kB / 90 kB")
    #expect(rows[0].endpoint == "192.0.2.10:51820")
}

@Test func rowsKeepTheOrderThePublisherSentThem() {
    // The terminal lists them in view order, and two tools showing the same
    // tunnels in different orders is one of them being wrong.
    let rows = TunnelTable.rows(
        [up("charlie"), up("alpha"), up("bravo")], now: now, locale: english)

    #expect(rows.map(\.name) == ["charlie", "alpha", "bravo"])
}

@Test func aDownTunnelHasNothingToShowButItsEndpoint() {
    // It has no interface, so no handshake, no counters and no latency. A dash
    // in those cells reads like data.
    let down = TunnelStatus(
        name: "bravo", group: "extra", health: .down, endpoint: "bravo.example:51820")

    let row = TunnelTable.rows([down], pings: ["bravo": Ping(tunnel: "bravo", rtt: .milliseconds(9))],
        now: now, locale: english)[0]

    #expect(row.handshake.isEmpty)
    #expect(row.traffic.isEmpty)
    #expect(row.ping == .none)
    #expect(row.endpoint == "bravo.example:51820")
}

@Test func aTunnelWithNoEndpointConfiguredSaysSo() {
    let row = TunnelTable.rows([up("alpha", endpoint: nil)], now: now, locale: english)[0]

    #expect(row.endpoint == "—")
}

@Test func aTableRowForATunnelInNoGroupSaysSo() {
    let row = TunnelTable.rows([up("alpha", group: "")], now: now, locale: english)[0]

    #expect(row.group == "no group")
}

@Test func aMeasuredPingIsShownInWholeMilliseconds() {
    // A tenth of a millisecond over a tunnel is noise dressed as precision.
    let row = TunnelTable.rows(
        [up("alpha")], pings: ["alpha": Ping(tunnel: "alpha", rtt: .milliseconds(18.4))],
        now: now, locale: english)[0]

    #expect(row.ping == .rtt("18ms"))
}

@Test func aFailedPingKeepsItsReasonRatherThanBecomingAZero() {
    let row = TunnelTable.rows(
        [up("alpha")], pings: ["alpha": Ping(tunnel: "alpha", error: "timeout")],
        now: now, locale: english)[0]

    #expect(row.ping == .failed(reason: "timeout"))
}

@Test func aTunnelNobodyProbedHasAnEmptyLatencyCell() {
    // Distinct from a probe that failed: one says "we did not ask", the other
    // says "we asked and got nothing", and they are not the same news.
    let row = TunnelTable.rows([up("alpha")], now: now, locale: english)[0]

    #expect(row.ping == .none)
}

@Test func aPingForAnotherTunnelIsNotShownOnThisOne() {
    let row = TunnelTable.rows(
        [up("alpha")], pings: ["bravo": Ping(tunnel: "bravo", rtt: .milliseconds(9))],
        now: now, locale: english)[0]

    #expect(row.ping == .none)
}

@Test func aWatchedTunnelShowsTheFreshCountersRatherThanTheViewsOwn() {
    // The view's counters are minutes old by the time anybody reads them, and
    // showing those beside a chart drawn from fresher ones is the window
    // disagreeing with itself.
    let sample = Sample(tunnel: "alpha", at: now, rx: 1_048_576, tx: 524_288)

    let row = TunnelTable.rows(
        [up("alpha")], latest: ["alpha": sample], now: now, locale: english)[0]

    #expect(row.traffic == "1 MB / 512 kB")
}

@Test func aTunnelThatNeverShookHandsHasAnEmptyHandshakeCell() {
    let row = TunnelTable.rows([up("alpha", handshake: nil)], now: now, locale: english)[0]

    #expect(row.handshake.isEmpty)
}
