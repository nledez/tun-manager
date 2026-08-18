import Foundation
import Testing

@testable import TunManagerFeed

private let anInstant = Date(timeIntervalSince1970: 1_786_968_191)

private func aSnapshot(_ names: String...) -> Snapshot {
    Snapshot(
        context: FeedContext(name: "office"),
        taken: anInstant,
        tunnels: names.map { TunnelStatus(name: $0, group: "needed", health: .up) })
}

@Test func theMachineAsksToConnectAsSoonAsItIsStarted() {
    var machine = LinkMachine()

    #expect(machine.handle(.start) == [.connect])
    #expect(machine.state == .connecting)
}

@Test func aConnectThatFailsBecauseThereIsNoSocketReportsThatTunManagerIsNotRunning() {
    var machine = LinkMachine()
    _ = machine.handle(.start)

    _ = machine.handle(.connectFailed(ENOENT))

    #expect(machine.reason == .notRunning)
}

@Test func aConnectThatIsRefusedReportsASocketLeftBehindByACrash() {
    var machine = LinkMachine()
    _ = machine.handle(.start)

    _ = machine.handle(.connectFailed(ECONNREFUSED))

    #expect(machine.reason == .refused)
}

@Test func aConnectThatIsNotPermittedReportsASocketThatBelongsToSomebodyElse() {
    // No SUDO_USER, so the publisher never handed the socket over and it is
    // still root's. Only a human restarting tun-manager differently fixes it.
    for code in [EACCES, EPERM] {
        var machine = LinkMachine()
        _ = machine.handle(.start)

        _ = machine.handle(.connectFailed(code))

        #expect(machine.reason == .notPermitted)
    }
}

@Test func aHelloWithTheSchemaThisAppKnowsMakesTheLinkLive() {
    var machine = LinkMachine()
    _ = machine.handle(.start)

    _ = machine.handle(.message(.hello(schema: 1, version: "v0.2.0")))

    #expect(machine.isLive)
    #expect(machine.publisherVersion == "v0.2.0")
}

@Test func aHelloWithASchemaThisAppDoesNotKnowBlocksTheLinkAndSchedulesNoRetry() {
    // Retrying against a condition only a human can clear is how a program ends
    // up logging the same line every thirty seconds forever.
    var machine = LinkMachine()
    _ = machine.handle(.start)

    let actions = machine.handle(.message(.hello(schema: 4, version: "v9.0.0")))

    #expect(actions == [.closeConnection])
    #expect(machine.state == .blocked(theirSchema: 4))
}

@Test func aBlockedLinkReconnectsOnlyWhenTheUserAsks() {
    var machine = LinkMachine()
    _ = machine.handle(.start)
    _ = machine.handle(.message(.hello(schema: 4, version: "v9.0.0")))

    #expect(machine.handle(.systemDidWake).isEmpty)
    #expect(machine.handle(.menuWillOpen).isEmpty)
    #expect(machine.handle(.userAskedToRetry) == [.connect])
}

@Test func aConnectionThatEndsBeforeHelloDoesNotResetTheAttemptCounter() {
    // This is the shutdown race in feed.Server.add: while tun-manager is going
    // away it accepts a connection and closes it without a word. A client that
    // reset its backoff on a successful connect(2) would spin at full speed for
    // the whole shutdown.
    var machine = LinkMachine()
    _ = machine.handle(.start)

    // Two rejections in a row. Comparing them to each other rather than to a
    // connect failure matters: each reason has its own base delay, so a
    // cross-reason comparison would prove nothing about the counter.
    _ = machine.handle(.endOfStream)
    let first = machine.retryDelay
    _ = machine.handle(.retryTimerFired)
    _ = machine.handle(.endOfStream)
    let second = machine.retryDelay

    #expect(machine.reason == .rejected)
    #expect(first == ReconnectPolicy.delay(after: .rejected, attempt: 0))
    #expect(second == ReconnectPolicy.delay(after: .rejected, attempt: 1))
    #expect(second != first, "the backoff did not grow: the counter was reset")
}

@Test func anAcceptedHelloIsTheOnlyThingThatResetsTheAttemptCounter() {
    var machine = LinkMachine()
    _ = machine.handle(.start)
    for _ in 0..<4 {
        _ = machine.handle(.connectFailed(ENOENT))
        _ = machine.handle(.retryTimerFired)
    }

    _ = machine.handle(.message(.hello(schema: 1, version: "v0.2.0")))
    _ = machine.handle(.endOfStream)

    #expect(machine.retryDelay == ReconnectPolicy.delay(after: .lost, attempt: 0))
}

@Test func byeIsFollowedByATwoSecondRetryBecauseRestartingTunManagerIsWhyItWasSent() {
    var machine = LinkMachine()
    _ = machine.handle(.start)
    _ = machine.handle(.message(.hello(schema: 1, version: "v0.2.0")))

    let actions = machine.handle(.message(.bye))

    #expect(machine.reason == .goodbye)
    #expect(machine.retryDelay == .seconds(2))
    #expect(actions.contains(.closeConnection))
}

@Test func aBareEndOfStreamAfterALiveConnectionRetriesFromTheTopOfTheLadder() {
    // Either the publisher crashed or we were dropped for falling behind, and
    // the client cannot tell. The second recovers instantly, so guess that way.
    var machine = LinkMachine()
    _ = machine.handle(.start)
    _ = machine.handle(.message(.hello(schema: 1, version: "v0.2.0")))

    _ = machine.handle(.endOfStream)

    #expect(machine.reason == .lost)
    #expect(machine.retryDelay == .milliseconds(250))
}

@Test func theLastSnapshotSurvivesADisconnectionSoTheMenuDoesNotGoBlank() {
    var machine = LinkMachine()
    _ = machine.handle(.start)
    _ = machine.handle(.message(.hello(schema: 1, version: "v0.2.0")))
    _ = machine.handle(.message(.state(aSnapshot("alpha", "bravo"))))

    _ = machine.handle(.endOfStream)

    #expect(machine.snapshot?.tunnels.count == 2)
}

@Test func aLiveLinkThatHasNotYetSeenAStateSaysSoInsteadOfShowingNothing() {
    // A freshly started publisher sends hello and then nothing until its first
    // refresh. "Connected, waiting" is a different sentence from an empty menu.
    var machine = LinkMachine()
    _ = machine.handle(.start)
    _ = machine.handle(.message(.hello(schema: 1, version: "v0.2.0")))

    #expect(machine.state == .live(sawState: false))

    _ = machine.handle(.message(.state(aSnapshot("alpha"))))
    #expect(machine.state == .live(sawState: true))
}

@Test func openingTheMenuOnALiveLinkSendsARefresh() {
    var machine = LinkMachine()
    _ = machine.handle(.start)
    _ = machine.handle(.message(.hello(schema: 1, version: "v0.2.0")))

    #expect(machine.handle(.menuWillOpen) == [.send(.refresh)])
}

@Test func openingTheMenuWhileRetryingReconnectsInsteadOfWaitingOutTheBackoff() {
    // The ceiling is thirty seconds, which is only defensible because looking
    // at the menu bar is itself a reason to try again.
    for event in [LinkEvent.menuWillOpen, .systemDidWake, .userAskedToRetry] {
        var machine = LinkMachine()
        _ = machine.handle(.start)
        _ = machine.handle(.connectFailed(ENOENT))

        let actions = machine.handle(event)

        #expect(actions == [.cancelRetry, .connect], "\(event) did not reconnect")
        #expect(machine.state == .connecting)
    }
}

@Test func stoppingCancelsThePendingRetryAndClosesTheConnection() {
    var machine = LinkMachine()
    _ = machine.handle(.start)
    _ = machine.handle(.message(.hello(schema: 1, version: "v0.2.0")))

    #expect(machine.handle(.stop) == [.cancelRetry, .closeConnection])
    #expect(machine.state == .idle)
}

@Test func aSampleLineOnALinkThatNeverWatchedAnythingIsIgnored() {
    var machine = LinkMachine()
    _ = machine.handle(.start)
    _ = machine.handle(.message(.hello(schema: 1, version: "v0.2.0")))

    let sample = Sample(tunnel: "alpha", at: anInstant, rx: 1, tx: 1)
    #expect(machine.handle(.message(.sample(sample))).isEmpty)
    #expect(machine.isLive)
}

@Test func aStateArrivingIsPublishedWithWhatChangedSinceTheLastOne() {
    var machine = LinkMachine()
    _ = machine.handle(.start)
    _ = machine.handle(.message(.hello(schema: 1, version: "v0.2.0")))
    _ = machine.handle(.message(.state(aSnapshot("alpha"))))

    let actions = machine.handle(.message(.state(aSnapshot("alpha", "bravo"))))

    guard case .publish(_, let diff)? = actions.first else {
        Issue.record("nothing was published: \(actions)")
        return
    }
    #expect(diff.appeared == ["bravo"])
}
