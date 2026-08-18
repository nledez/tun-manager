import Foundation
import Testing

@testable import TunManagerFeed

// MARK: - The wire

@Test func aPingLineYieldsOneResultPerTunnelProbed() throws {
    guard case .ping(let pings)? = FeedDecoder.decode(Fixtures.line(Fixtures.ping)) else {
        Issue.record("not a ping line")
        return
    }

    #expect(pings.map(\.tunnel) == ["alpha", "bravo"])
    #expect(pings[0].rtt == .milliseconds(18.4))
    #expect(pings[0].error == nil)
}

@Test func aProbeThatFailedCarriesTheReasonAndNoTime() throws {
    // Zero milliseconds is a measurement. "No answer" is not, and a client that
    // renders one as the other says the tunnel is perfect when it is dead.
    guard case .ping(let pings)? = FeedDecoder.decode(Fixtures.line(Fixtures.ping)) else {
        Issue.record("not a ping line")
        return
    }

    #expect(pings[1].rtt == nil)
    #expect(pings[1].error == "timeout")
}

@Test func aPingLineWithNoResultsDecodesAsAnEmptyRound() throws {
    let got = FeedDecoder.decode(Fixtures.line(#"{"type":"ping","results":[]}"#))

    #expect(got == .ping([]))
}

@Test func aPingLineWhoseResultsAreNullIsRefused() throws {
    // Same rule as a state line: the publisher never writes null here, so a
    // null is a broken line rather than a round that measured nothing.
    #expect(FeedDecoder.decode(Fixtures.line(#"{"type":"ping","results":null}"#)) == nil)
}

// MARK: - The verb

@Test func askingForAPingNamesTheTunnelItWants() throws {
    let object = try JSONSerialization.jsonObject(with: ClientCommand.ping("alpha").line)

    #expect((object as? [String: String]) == ["type": "ping", "tunnel": "alpha"])
}

@Test func askingForAPingWithNoNameCoversEveryTunnel() throws {
    // The publisher reads a missing name as "all of them", so the key must be
    // left out rather than sent as an empty string, which names nothing.
    let object = try JSONSerialization.jsonObject(with: ClientCommand.ping(nil).line)

    #expect((object as? [String: String]) == ["type": "ping"])
}

@Test func aPingRequestEndsWithANewlineBecauseThatIsTheFraming() {
    #expect(String(decoding: ClientCommand.ping(nil).line, as: UTF8.self).hasSuffix("\n"))
}
