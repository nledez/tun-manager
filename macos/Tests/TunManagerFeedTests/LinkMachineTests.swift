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
    var machine = Proven.machine()

    #expect(machine.handle(.start) == [.connect])
    #expect(machine.state == .connecting)
}

@Test func aConnectThatFailsBecauseThereIsNoSocketReportsThatTunManagerIsNotRunning() {
    var machine = Proven.machine()
    _ = machine.handle(.start)

    _ = machine.handle(.connectFailed(ENOENT))

    #expect(machine.reason == .notRunning)
}

@Test func aConnectThatIsRefusedReportsASocketLeftBehindByACrash() {
    var machine = Proven.machine()
    _ = machine.handle(.start)

    _ = machine.handle(.connectFailed(ECONNREFUSED))

    #expect(machine.reason == .refused)
}

@Test func aConnectThatIsNotPermittedReportsASocketThatBelongsToSomebodyElse() {
    // No SUDO_USER, so the publisher never handed the socket over and it is
    // still root's. Only a human restarting tun-manager differently fixes it.
    for code in [EACCES, EPERM] {
        var machine = Proven.machine()
        _ = machine.handle(.start)

        _ = machine.handle(.connectFailed(code))

        #expect(machine.reason == .notPermitted)
    }
}

@Test func aHelloWithTheSchemaThisAppKnowsMakesTheLinkLive() {
    var machine = Proven.machine()
    _ = machine.handle(.start)

    _ = Proven.greet(&machine)

    #expect(machine.isLive)
    #expect(machine.publisherVersion == Proven.version)
}

@Test func aHelloWithASchemaThisAppDoesNotKnowBlocksTheLinkAndSchedulesNoRetry() {
    // Retrying against a condition only a human can clear is how a program ends
    // up logging the same line every thirty seconds forever.
    var machine = Proven.machine()
    _ = machine.handle(.start)

    let actions = machine.handle(.message(.hello(schema: 4, version: "v9.0.0", publicKey: nil)))

    #expect(actions == [.closeConnection])
    #expect(machine.state == .blocked(theirSchema: 4))
}

@Test func aBlockedLinkReconnectsOnlyWhenTheUserAsks() {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = machine.handle(.message(.hello(schema: 4, version: "v9.0.0", publicKey: nil)))

    #expect(machine.handle(.systemDidWake).isEmpty)
    #expect(machine.handle(.menuWillOpen).isEmpty)
    #expect(machine.handle(.userAskedToRetry) == [.connect])
}

@Test func aConnectionThatEndsBeforeHelloDoesNotResetTheAttemptCounter() {
    // This is the shutdown race in feed.Server.add: while tun-manager is going
    // away it accepts a connection and closes it without a word. A client that
    // reset its backoff on a successful connect(2) would spin at full speed for
    // the whole shutdown.
    var machine = Proven.machine()
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
    var machine = Proven.machine()
    _ = machine.handle(.start)
    for _ in 0..<4 {
        _ = machine.handle(.connectFailed(ENOENT))
        _ = machine.handle(.retryTimerFired)
    }

    _ = Proven.greet(&machine)
    _ = machine.handle(.endOfStream)

    #expect(machine.retryDelay == ReconnectPolicy.delay(after: .lost, attempt: 0))
}

@Test func byeIsFollowedByATwoSecondRetryBecauseRestartingTunManagerIsWhyItWasSent() {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)

    let actions = machine.handle(.message(.bye))

    #expect(machine.reason == .goodbye)
    #expect(machine.retryDelay == .seconds(2))
    #expect(actions.contains(.closeConnection))
}

@Test func aBareEndOfStreamAfterALiveConnectionRetriesFromTheTopOfTheLadder() {
    // Either the publisher crashed or we were dropped for falling behind, and
    // the client cannot tell. The second recovers instantly, so guess that way.
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)

    _ = machine.handle(.endOfStream)

    #expect(machine.reason == .lost)
    #expect(machine.retryDelay == .milliseconds(250))
}

@Test func theLastSnapshotSurvivesADisconnectionSoTheMenuDoesNotGoBlank() {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)
    _ = machine.handle(.message(.state(aSnapshot("alpha", "bravo"))))

    _ = machine.handle(.endOfStream)

    #expect(machine.snapshot?.tunnels.count == 2)
}

@Test func aLiveLinkThatHasNotYetSeenAStateSaysSoInsteadOfShowingNothing() {
    // A freshly started publisher sends hello and then nothing until its first
    // refresh. "Connected, waiting" is a different sentence from an empty menu.
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)

    #expect(machine.state == .live(sawState: false))

    _ = machine.handle(.message(.state(aSnapshot("alpha"))))
    #expect(machine.state == .live(sawState: true))
}

@Test func openingTheMenuOnALiveLinkSendsARefresh() {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)

    #expect(machine.handle(.menuWillOpen) == [.send(.refresh)])
}

@Test func openingTheMenuWhileRetryingReconnectsInsteadOfWaitingOutTheBackoff() {
    // The ceiling is thirty seconds, which is only defensible because looking
    // at the menu bar is itself a reason to try again.
    for event in [LinkEvent.menuWillOpen, .systemDidWake, .userAskedToRetry] {
        var machine = Proven.machine()
        _ = machine.handle(.start)
        _ = machine.handle(.connectFailed(ENOENT))

        let actions = machine.handle(event)

        #expect(actions == [.cancelRetry, .connect], "\(event) did not reconnect")
        #expect(machine.state == .connecting)
    }
}

@Test func stoppingCancelsThePendingRetryAndClosesTheConnection() {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)

    #expect(machine.handle(.stop) == [.cancelRetry, .closeConnection])
    #expect(machine.state == .idle)
}

@Test func aSampleLineOnALinkThatNeverWatchedAnythingIsIgnored() {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)

    let sample = Sample(tunnel: "alpha", at: anInstant, rx: 1, tx: 1)
    #expect(machine.handle(.message(.sample(sample))).isEmpty)
    #expect(machine.isLive)
}

@Test func aStateArrivingIsPublishedWithWhatChangedSinceTheLastOne() {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)
    _ = machine.handle(.message(.state(aSnapshot("alpha"))))

    let actions = machine.handle(.message(.state(aSnapshot("alpha", "bravo"))))

    guard case .publish(_, let diff)? = actions.first else {
        Issue.record("nothing was published: \(actions)")
        return
    }
    #expect(diff.appeared == ["bravo"])
}

// Watching a tunnel is a subscription held by the window, and a subscription
// has to survive the link going away underneath it.

@Test func askingToWatchATunnelSendsTheVerb() {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)

    #expect(machine.handle(.watch("alpha")) == [.send(.watch("alpha"))])
}

@Test func watchingASecondTunnelKeepsTheFirst() {
    // Switching tunnels in the window must not cost the first one's history:
    // going back to it should show a continuous graph, not a gap where nobody
    // happened to be looking.
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)
    _ = machine.handle(.watch("alpha"))

    #expect(machine.handle(.watch("bravo")) == [.send(.watch("bravo"))])
    #expect(machine.watched == ["alpha", "bravo"])
}

@Test func watchingTheTunnelAlreadyWatchedSaysNothingTwice() {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)
    _ = machine.handle(.watch("alpha"))

    #expect(machine.handle(.watch("alpha")).isEmpty)
}

@Test func closingTheWindowReleasesEveryTunnelItLookedAt() {
    // Nobody is looking any more, so tun-manager should stop reading counters
    // for any of them.
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)
    _ = machine.handle(.watch("bravo"))
    _ = machine.handle(.watch("alpha"))

    #expect(machine.handle(.watchNothing) == [.send(.unwatch("alpha")), .send(.unwatch("bravo"))])
    #expect(machine.watched.isEmpty)
    #expect(machine.handle(.watchNothing).isEmpty)
}

@Test func everyWatchedTunnelIsRenewedTogetherAfterAReconnection() {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)
    _ = machine.handle(.watch("bravo"))
    _ = machine.handle(.watch("alpha"))

    _ = machine.handle(.endOfStream)
    _ = machine.handle(.retryTimerFired)

    let onHello = Proven.greet(&machine)

    #expect(onHello.contains(.send(.watch("alpha"))))
    #expect(onHello.contains(.send(.watch("bravo"))))
}

@Test func aWatchAskedForWhileTheLinkIsDownIsSentOnceItComesBack() {
    // The window can be opened before tun-manager is running, and the
    // subscription has to be waiting rather than lost.
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = machine.handle(.connectFailed(ENOENT))

    #expect(machine.handle(.watch("alpha")).isEmpty)

    _ = machine.handle(.retryTimerFired)
    let onHello = Proven.greet(&machine)

    #expect(onHello.contains(.send(.watch("alpha"))))
}

@Test func aWatchIsRenewedAfterTheLinkDropsAndComesBack() {
    // The publisher forgets every watch when the connection ends, so a window
    // left open across a restart would show a graph frozen at the moment the
    // link died, with nothing saying why.
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)
    _ = machine.handle(.watch("alpha"))

    _ = machine.handle(.endOfStream)
    _ = machine.handle(.retryTimerFired)
    let onHello = Proven.greet(&machine)

    #expect(onHello.contains(.send(.watch("alpha"))))
}

@Test func nothingIsRenewedWhenNoWindowIsOpen() {
    var machine = Proven.machine()
    _ = machine.handle(.start)

    let onHello = Proven.greet(&machine)

    #expect(onHello.contains { if case .send(.watch) = $0 { return true } else { return false } } == false)
}

@Test func aSampleForTheWatchedTunnelIsHandedOn() {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)
    _ = machine.handle(.watch("alpha"))

    let sample = Sample(tunnel: "alpha", at: anInstant, rx: 100, tx: 50)
    #expect(machine.handle(.message(.sample(sample))) == [.publishSample(sample)])
}

@Test func aSampleForATunnelNobodyIsWatchingIsDropped() {
    // An unwatch and a reading can cross on the wire. Charting one for a tunnel
    // the window has already left would draw it into the wrong graph.
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = Proven.greet(&machine)
    _ = machine.handle(.watch("alpha"))

    let stray = Sample(tunnel: "charlie", at: anInstant, rx: 100, tx: 50)
    #expect(machine.handle(.message(.sample(stray))).isEmpty)
}

// MARK: - Pings

/// A machine that has connected and been greeted.
private func aLiveMachine() -> LinkMachine {
    var machine = Proven.machine()
    _ = machine.handle(.start)
    // Greeted and proved: a hello alone leaves the link waiting for the
    // publisher to say which one it is.
    _ = Proven.greet(&machine)
    return machine
}

@Test func askingForAPingSendsTheVerbWhileTheLinkIsLive() {
    var machine = aLiveMachine()

    #expect(machine.handle(.askForPing("alpha")) == [.send(.ping("alpha"))])
}

@Test func aPingEventWithNoNameSendsTheVerbWithNoName() {
    var machine = aLiveMachine()

    #expect(machine.handle(.askForPing(nil)) == [.send(.ping(nil))])
}

@Test func askingForAPingWhileDisconnectedIsDroppedRatherThanQueued() {
    // A watch is restored on the next hello because it is a standing
    // subscription. A probe is a question about right now, and answering it two
    // minutes later would answer a different question.
    var machine = Proven.machine()
    _ = machine.handle(.start)
    _ = machine.handle(.connectFailed(ENOENT))

    #expect(machine.handle(.askForPing("alpha")).isEmpty)
}

@Test func aPingIsNotResentOnTheNextHelloTheWayAWatchIs() {
    var machine = aLiveMachine()
    _ = machine.handle(.askForPing("alpha"))
    _ = machine.handle(.endOfStream)
    _ = machine.handle(.retryTimerFired)

    let onHello = Proven.greet(&machine)

    #expect(onHello.contains { if case .send(.ping) = $0 { return true } else { return false } } == false)
}

@Test func aRoundOfProbesIsKeptForWhoeverDraws() {
    var machine = aLiveMachine()

    _ = machine.handle(.message(.ping([Ping(tunnel: "alpha", rtt: .milliseconds(18))])))

    #expect(machine.pings["alpha"]?.rtt == .milliseconds(18))
}

@Test func aRoundCoveringOneTunnelDoesNotBlankTheOthers() {
    // Asking about one tunnel is the common case: the window is showing it.
    var machine = aLiveMachine()
    _ = machine.handle(
        .message(
            .ping([
                Ping(tunnel: "alpha", rtt: .milliseconds(18)),
                Ping(tunnel: "bravo", rtt: .milliseconds(31)),
            ])))

    _ = machine.handle(.message(.ping([Ping(tunnel: "alpha", rtt: .milliseconds(20))])))

    #expect(machine.pings["alpha"]?.rtt == .milliseconds(20))
    #expect(machine.pings["bravo"]?.rtt == .milliseconds(31))
}

@Test func aTunnelThatLeavesTheConfigurationTakesItsLatencyWithIt() {
    // Otherwise a name reused later inherits a measurement of something else.
    var machine = aLiveMachine()
    _ = machine.handle(.message(.state(aSnapshot("alpha", "bravo"))))
    _ = machine.handle(.message(.ping([Ping(tunnel: "bravo", rtt: .milliseconds(31))])))

    _ = machine.handle(.message(.state(aSnapshot("alpha"))))

    #expect(machine.pings["bravo"] == nil)
}

@Test func aMeasuredLatencySurvivesADisconnectionTheWayTheSnapshotDoes() {
    var machine = aLiveMachine()
    _ = machine.handle(.message(.ping([Ping(tunnel: "alpha", rtt: .milliseconds(18))])))

    _ = machine.handle(.endOfStream)

    #expect(machine.pings["alpha"]?.rtt == .milliseconds(18))
}
