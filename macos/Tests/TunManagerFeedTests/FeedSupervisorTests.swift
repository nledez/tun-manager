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

@MainActor
@Test func theObserverIsHandedWhatChangedBetweenViews() async {
    // The diff cannot be rebuilt from the current snapshot once it has replaced
    // the one before it, so it travels with the publish rather than being
    // recomputed by whoever wants it.
    final class Recorder: FeedObserver {
        var diffs: [SnapshotDiff] = []
        func linkDidChange(state: LinkState, snapshot: Snapshot?, publisherVersion: String?) {}
        func linkDidPublish(snapshot: Snapshot, diff: SnapshotDiff) { diffs.append(diff) }
    }
    let recorder = Recorder()
    let transport = FakeTransport([
        .deliverAndStayOpen([Fixtures.hello + "\n", Fixtures.state + "\n"])
    ])
    let supervisor = FeedSupervisor(transport: transport)
    supervisor.observer = recorder

    supervisor.start()

    await eventually("the first publish") { !recorder.diffs.isEmpty }
    // The first view has nothing to compare against, so every tunnel is an
    // appearance and nothing is a health change — which is what keeps a burst
    // of banners off the screen at launch.
    #expect(recorder.diffs[0].appeared == ["alpha", "bravo"])
    #expect(recorder.diffs[0].healthChanges.isEmpty)
}

@MainActor
@Test func watchingATunnelPutsTheVerbOnTheWire() async {
    let transport = FakeTransport([
        .deliverAndStayOpen([Fixtures.hello + "\n", Fixtures.state + "\n"])
    ])
    let supervisor = FeedSupervisor(transport: transport)
    supervisor.start()
    await eventually("the link to come up") { supervisor.state.isLive }

    supervisor.watch("alpha")

    #expect(transport.sent.contains { $0.contains("\"watch\"") && $0.contains("alpha") })
}

@MainActor
@Test func closingTheWindowReleasesTheTunnelOnTheWire() async {
    let transport = FakeTransport([
        .deliverAndStayOpen([Fixtures.hello + "\n", Fixtures.state + "\n"])
    ])
    let supervisor = FeedSupervisor(transport: transport)
    supervisor.start()
    await eventually("the link to come up") { supervisor.state.isLive }
    supervisor.watch("alpha")

    supervisor.watchNothing()

    #expect(transport.sent.contains { $0.contains("\"unwatch\"") })
}

@MainActor
@Test func aReadingForTheWatchedTunnelReachesTheObserver() async {
    // The whole point of watching: the numbers have to arrive somewhere.
    final class Recorder: FeedObserver {
        var samples: [Sample] = []
        func linkDidChange(state: LinkState, snapshot: Snapshot?, publisherVersion: String?) {}
        func linkDidSample(_ sample: Sample) { samples.append(sample) }
    }
    let recorder = Recorder()
    let transport = FakeTransport([
        .deliverAndStayOpen([Fixtures.hello + "\n", Fixtures.state + "\n"])
    ])
    let supervisor = FeedSupervisor(transport: transport)
    supervisor.observer = recorder
    supervisor.start()
    await eventually("the link to come up") { supervisor.state.isLive }

    // Watched first, then the reading arrives — the order the publisher uses,
    // and the order that matters: a reading for a tunnel nobody asked about is
    // dropped on purpose.
    supervisor.watch("alpha")
    transport.push(
        #"{"type":"sample","tunnel":"alpha","at":"2026-08-17T14:03:12.1+02:00","rx":9,"tx":4}"# + "\n")
    await eventually("the reading") { !recorder.samples.isEmpty }

    #expect(recorder.samples[0].rx == 9)
}

@MainActor
@Test func askingForAPingPutsTheVerbOnTheWire() async {
    let transport = FakeTransport([.deliverAndStayOpen([Fixtures.hello + "\n"])])
    let supervisor = FeedSupervisor(transport: transport)
    supervisor.start()
    await eventually("the link to go live") { supervisor.state == .live(sawState: false) }

    supervisor.askForPing("alpha")

    // Through the JSON rather than the bytes: field order carries no meaning
    // there, the publisher unmarshals into a struct, and JSONEncoder sorts.
    await eventually("the verb to be sent") { !transport.sent.isEmpty }
    let fields = transport.sent.map {
        (try? JSONSerialization.jsonObject(with: Data($0.utf8))) as? [String: String]
    }
    #expect(fields.contains(["type": "ping", "tunnel": "alpha"]))
}

@MainActor
@Test func aRoundOfProbesReachesWhoeverDraws() async {
    let transport = FakeTransport([.deliverAndStayOpen([Fixtures.hello + "\n"])])
    let supervisor = FeedSupervisor(transport: transport)
    supervisor.start()
    await eventually("the link to go live") { supervisor.state == .live(sawState: false) }

    transport.push(Fixtures.ping + "\n")
    await eventually("the round of probes") { supervisor.pings["alpha"] != nil }

    #expect(supervisor.pings["alpha"]?.rtt == .milliseconds(18.4))
    #expect(supervisor.pings["bravo"]?.error == "timeout")
}
