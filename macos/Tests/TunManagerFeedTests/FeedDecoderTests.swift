import Foundation
import Testing

@testable import TunManagerFeed

// These pin the wire contract line by line against internal/wire/wire.go. When
// one of them fails, the two sides have stopped agreeing.

@Test func aHelloLineYieldsTheSchemaAndVersionItCarries() throws {
    let got = FeedDecoder.decode(Fixtures.line(Fixtures.hello))

    #expect(
        got
            == .hello(
                schema: 2, version: "v0.6.0",
                publicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="))
}

@Test func aStateLineYieldsEveryTunnelInTheOrderThePublisherSentThem() throws {
    guard case .state(let snapshot)? = FeedDecoder.decode(Fixtures.line(Fixtures.state)) else {
        Issue.record("not a state line")
        return
    }

    #expect(snapshot.tunnels.map(\.name) == ["alpha", "bravo"])
    #expect(snapshot.context.name == "office")
    #expect(snapshot.context.interface == "en0")
    #expect(snapshot.context.address == "198.51.100.42")

    let alpha = snapshot.tunnels[0]
    #expect(alpha.health == .up)
    #expect(alpha.group == "needed")
    #expect(alpha.device == "utun7")
    #expect(alpha.endpoint == "192.0.2.10:51820")
    #expect(alpha.checkIP == "10.20.30.1")
    #expect(alpha.rxBytes == 184_320)
    #expect(alpha.txBytes == 92_160)
    #expect(alpha.lastHandshake != nil)
}

@Test func aTunnelWithNoDeviceEndpointCheckIPOrHandshakeDecodesWithThoseAbsent() throws {
    guard case .state(let snapshot)? = FeedDecoder.decode(Fixtures.line(Fixtures.state)) else {
        Issue.record("not a state line")
        return
    }

    let bravo = snapshot.tunnels[1]
    #expect(bravo.device == nil)
    #expect(bravo.checkIP == nil)
    #expect(bravo.lastHandshake == nil)
    // The counters are never omitted, zero included: health is what says a
    // tunnel is carrying nothing, not the absence of a field.
    #expect(bravo.rxBytes == 0)
    #expect(bravo.txBytes == 0)
    #expect(bravo.group == "")
}

@Test func aContextWithNoInterfaceOrAddressDecodesWithThoseAbsent() throws {
    guard case .state(let snapshot)? = FeedDecoder.decode(Fixtures.line(Fixtures.emptyState)) else {
        Issue.record("not a state line")
        return
    }

    #expect(snapshot.context.interface == nil)
    #expect(snapshot.context.address == nil)
    #expect(snapshot.tunnels.isEmpty)
}

@Test func aHealthValueThisClientHasNeverHeardOfDecodesAsUnknownRatherThanLosingTheLine() throws {
    // A publisher that grows a fourth health should cost us one row we cannot
    // colour, not the whole view.
    let line = Fixtures.state.replacingOccurrences(of: #""health":"up""#, with: #""health":"reticulating""#)

    guard case .state(let snapshot)? = FeedDecoder.decode(Fixtures.line(line)) else {
        Issue.record("the line was lost")
        return
    }

    #expect(snapshot.tunnels[0].health == .unknown("reticulating"))
}

@Test func aLineWhoseTypeIsUnknownIsIgnored() {
    #expect(FeedDecoder.decode(Fixtures.line(#"{"type":"weather","outlook":"grim"}"#)) == nil)
}

@Test func aLineThatIsNotJSONIsIgnored() {
    for text in ["", "not json", "{", "[1,2,3]"] {
        #expect(FeedDecoder.decode(Fixtures.line(text)) == nil, "\(text) was decoded")
    }
}

@Test func aStateLineWhoseTunnelsAreNullIsRefusedBecauseAnEmptyMenuIsALie() {
    // The publisher builds the slice explicitly so it marshals as [] and never
    // as null. If null ever arrives, something is wrong, and rendering "no
    // tunnels" would be the worst possible way to say so.
    let line = #"{"type":"state","context":{"name":""},"taken":"2026-08-17T14:03:11Z","tunnels":null}"#

    #expect(FeedDecoder.decode(Fixtures.line(line)) == nil)
}

@Test func aByeLineIsRecognised() {
    #expect(FeedDecoder.decode(Fixtures.line(Fixtures.bye)) == .bye)
}

@Test func counterslargerThanA32BitIntegerDecode() throws {
    let line = Fixtures.state.replacingOccurrences(of: #""rx_bytes":184320"#, with: #""rx_bytes":4294967296"#)

    guard case .state(let snapshot)? = FeedDecoder.decode(Fixtures.line(line)) else {
        Issue.record("not a state line")
        return
    }

    #expect(snapshot.tunnels[0].rxBytes == 4_294_967_296)
}

@Test func aSampleLineDecodesEvenThoughThisVersionNeverAsksForOne() throws {
    // Version 1 never sends `watch`, so this cannot arrive — but a client that
    // crashed on a message it merely does not want would be a poor client.
    let line = #"{"type":"sample","tunnel":"alpha","at":"2026-08-17T14:03:12.004112+02:00","rx":184832,"tx":92416}"#

    guard case .sample(let sample)? = FeedDecoder.decode(Fixtures.line(line)) else {
        Issue.record("not a sample line")
        return
    }

    #expect(sample.tunnel == "alpha")
    #expect(sample.rx == 184_832)
}
