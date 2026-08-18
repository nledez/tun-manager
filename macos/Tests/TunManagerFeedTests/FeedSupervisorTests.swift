import Foundation
import Testing

@testable import TunManagerFeed

/// Waits for a condition rather than for a duration: the supervisor's work
/// happens in tasks, and sleeping a fixed amount is how a suite becomes flaky.
@MainActor
private func eventually(
    _ what: String, timeout: Duration = .seconds(2), _ condition: () -> Bool
) async {
    let deadline = ContinuousClock.now + timeout
    while ContinuousClock.now < deadline {
        if condition() { return }
        try? await Task.sleep(for: .milliseconds(2))
    }
    Issue.record("timed out waiting for \(what)")
}

@MainActor
@Test func aHelloAndAStateBringTheLinkUpAndFillTheSnapshot() async {
    let transport = FakeTransport([.deliver([Fixtures.hello + "\n", Fixtures.state + "\n"])])
    let supervisor = FeedSupervisor(transport: transport)

    supervisor.start()

    await eventually("the snapshot") { supervisor.snapshot != nil }
    #expect(supervisor.snapshot?.tunnels.map(\.name) == ["alpha", "bravo"])
    #expect(supervisor.publisherVersion == "v0.2.0")
}

@MainActor
@Test func aLineSplitAcrossChunksIsUnderstoodAllTheSame() async {
    // The framer is exercised through the supervisor here, because the seam
    // between reading and decoding is where a half-line would be lost.
    let whole = Fixtures.hello + "\n" + Fixtures.state + "\n"
    let cut = whole.index(whole.startIndex, offsetBy: 30)
    let transport = FakeTransport([.deliver([String(whole[..<cut]), String(whole[cut...])])])
    let supervisor = FeedSupervisor(transport: transport)

    supervisor.start()

    await eventually("the snapshot") { supervisor.snapshot != nil }
    #expect(supervisor.snapshot?.tunnels.count == 2)
}

@MainActor
@Test func aConnectionThatCannotBeOpenedIsRetriedRatherThanGivenUpOn() async {
    let transport = FakeTransport([.refuse(ENOENT), .deliverAndStayOpen([Fixtures.hello + "\n"])])
    let supervisor = FeedSupervisor(transport: transport)

    supervisor.start()

    await eventually("the second attempt") { transport.attempts >= 2 }
    await eventually("the link to come up") { supervisor.state.isLive }
}

@MainActor
@Test func openingTheMenuOnALiveLinkAsksForAFreshView() async {
    let transport = FakeTransport([.deliverAndStayOpen([Fixtures.hello + "\n", Fixtures.state + "\n"])])
    let supervisor = FeedSupervisor(transport: transport)
    supervisor.start()
    await eventually("the link to come up") { supervisor.state.isLive }

    supervisor.menuWillOpen()

    #expect(transport.sent == ["{\"type\":\"refresh\"}\n"])
}

@MainActor
@Test func theObserverIsToldWheneverAnythingChanges() async {
    final class Recorder: FeedObserver {
        var states: [LinkState] = []
        func linkDidChange(state: LinkState, snapshot: Snapshot?, publisherVersion: String?) {
            states.append(state)
        }
    }
    let recorder = Recorder()
    let transport = FakeTransport([.deliverAndStayOpen([Fixtures.hello + "\n", Fixtures.state + "\n"])])
    let supervisor = FeedSupervisor(transport: transport)
    supervisor.observer = recorder

    supervisor.start()

    await eventually("the live state") { recorder.states.contains(.live(sawState: true)) }
    #expect(recorder.states.first == .connecting)
}

@MainActor
@Test func stoppingEndsTheLinkAndStopsReconnecting() async {
    let transport = FakeTransport([.refuse(ENOENT)])
    let supervisor = FeedSupervisor(transport: transport)
    supervisor.start()
    await eventually("the first attempt") { transport.attempts >= 1 }

    supervisor.stop()
    let attemptsWhenStopped = transport.attempts
    try? await Task.sleep(for: .milliseconds(150))

    #expect(supervisor.state == .idle)
    #expect(transport.attempts == attemptsWhenStopped, "it kept reconnecting after being stopped")
}

@MainActor
@Test func aStreamThatFailsIsTreatedAsALostConnectionRatherThanAnOrderlyOne() async {
    let transport = FakeTransport([.deliverThenFail([Fixtures.hello + "\n"])])
    let supervisor = FeedSupervisor(transport: transport)

    supervisor.start()

    await eventually("the retry") {
        if case .retrying = supervisor.state { return true }
        return false
    }
}
