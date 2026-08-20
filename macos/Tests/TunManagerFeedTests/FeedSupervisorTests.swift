import Foundation
import Testing

@testable import TunManagerFeed

/// A supervisor that draws the nonce the fixture's auth line answers, dialling
/// the socket that answer names. Everything a publisher has to prove is in
/// those two, so a test that skipped them would be testing a link that cannot
/// happen.
@MainActor
private func proving(_ transport: FakeTransport) -> FeedSupervisor {
    FeedSupervisor(
        transport: transport, socketPath: Proven.socket, keys: NoPinnedKeys(),
        nonces: FixedNonce(Proven.nonce))
}

/// Waits for a condition rather than for a duration: the supervisor's work
/// happens in tasks, and sleeping a fixed amount is how a suite becomes flaky.
@MainActor
func eventually(
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
    let transport = FakeTransport([.deliver([Fixtures.hello + "\n" + Fixtures.auth + "\n", Fixtures.state + "\n"])])
    let supervisor = proving(transport)

    supervisor.start()

    await eventually("the snapshot") { supervisor.snapshot != nil }
    #expect(supervisor.snapshot?.tunnels.map(\.name) == ["alpha", "bravo"])
    #expect(supervisor.publisherVersion == Proven.version)
}

@MainActor
@Test func aLineSplitAcrossChunksIsUnderstoodAllTheSame() async {
    // The framer is exercised through the supervisor here, because the seam
    // between reading and decoding is where a half-line would be lost.
    let whole = Fixtures.hello + "\n" + Fixtures.auth + "\n" + Fixtures.state + "\n"
    let cut = whole.index(whole.startIndex, offsetBy: 30)
    let transport = FakeTransport([.deliver([String(whole[..<cut]), String(whole[cut...])])])
    let supervisor = proving(transport)

    supervisor.start()

    await eventually("the snapshot") { supervisor.snapshot != nil }
    #expect(supervisor.snapshot?.tunnels.count == 2)
}

@MainActor
@Test func aConnectionThatCannotBeOpenedIsRetriedRatherThanGivenUpOn() async {
    let transport = FakeTransport([.refuse(ENOENT), .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n"])])
    let supervisor = proving(transport)

    supervisor.start()

    await eventually("the second attempt") { transport.attempts >= 2 }
    await eventually("the link to come up") { supervisor.state.isLive }
}

@MainActor
@Test func openingTheMenuOnALiveLinkAsksForAFreshView() async {
    let transport = FakeTransport([.deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n", Fixtures.state + "\n"])])
    let supervisor = proving(transport)
    supervisor.start()
    await eventually("the link to come up") { supervisor.state.isLive }

    supervisor.menuWillOpen()

    // The challenge comes first on every connection: it is what the link is
    // waiting on before it shows anything.
    #expect(transport.sent.last == "{\"type\":\"refresh\"}\n")
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
    let transport = FakeTransport([.deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n", Fixtures.state + "\n"])])
    let supervisor = proving(transport)
    supervisor.observer = recorder

    supervisor.start()

    await eventually("the live state") { recorder.states.contains(.live(sawState: true)) }
    #expect(recorder.states.first == .connecting)
}

@MainActor
@Test func stoppingEndsTheLinkAndStopsReconnecting() async {
    let transport = FakeTransport([.refuse(ENOENT)])
    let supervisor = proving(transport)
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
    let transport = FakeTransport([.deliverThenFail([Fixtures.hello + "\n" + Fixtures.auth + "\n"])])
    let supervisor = proving(transport)

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
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n", Fixtures.state + "\n"])
    ])
    let supervisor = proving(transport)
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
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n", Fixtures.state + "\n"])
    ])
    let supervisor = proving(transport)
    supervisor.start()
    await eventually("the link to come up") { supervisor.state.isLive }

    supervisor.watch("alpha")

    #expect(transport.sent.contains { $0.contains("\"watch\"") && $0.contains("alpha") })
}

@MainActor
@Test func closingTheWindowReleasesTheTunnelOnTheWire() async {
    let transport = FakeTransport([
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n", Fixtures.state + "\n"])
    ])
    let supervisor = proving(transport)
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
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n", Fixtures.state + "\n"])
    ])
    let supervisor = proving(transport)
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
    let transport = FakeTransport([.deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n"])])
    let supervisor = proving(transport)
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
    let transport = FakeTransport([.deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n"])])
    let supervisor = proving(transport)
    supervisor.start()
    await eventually("the link to go live") { supervisor.state == .live(sawState: false) }

    transport.push(Fixtures.ping + "\n")
    await eventually("the round of probes") { supervisor.pings["alpha"] != nil }

    #expect(supervisor.pings["alpha"]?.rtt == .milliseconds(18.4))
    #expect(supervisor.pings["bravo"]?.error == "timeout")
}

@MainActor
@Test func thePublisherIsRememberedOnceItHasProvedItself() async {
    // Trust on first use: written where it survives a restart, so the next
    // connection is compared against it rather than believed afresh.
    let transport = FakeTransport([
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n"])
    ])
    let keys = RecordingKeys()
    let supervisor = FeedSupervisor(
        transport: transport, socketPath: Proven.socket, keys: keys,
        nonces: FixedNonce(Proven.nonce))

    supervisor.start()

    await eventually("the link to come up") { supervisor.state.isLive }
    #expect(keys.pinned(forSocket: Proven.socket) == Proven.key)
}

@MainActor
@Test func aPublisherThatCannotProveItselfIsNotRemembered() async {
    // The key on that socket is not the pinned one. Writing it down would be
    // this application agreeing with whoever is there.
    let transport = FakeTransport([
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n"])
    ])
    let keys = RecordingKeys()
    let other = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
    keys.pin(other, forSocket: Proven.socket)
    let supervisor = FeedSupervisor(
        transport: transport, socketPath: Proven.socket, keys: keys,
        nonces: FixedNonce(Proven.nonce))

    supervisor.start()

    await eventually("the refusal") {
        if case .unproven = supervisor.state { return true } else { return false }
    }
    #expect(keys.pinned(forSocket: Proven.socket) == other)
    #expect(supervisor.snapshot == nil)
}

/// A store in memory, so a test never touches the keychain of whoever runs it.
private final class RecordingKeys: PinnedKeys, @unchecked Sendable {
    private var keys: [String: String] = [:]

    func pinned(forSocket path: String) -> String? { keys[path] }
    func pin(_ key: String, forSocket path: String) { keys[path] = key }
    func forget(socket path: String) { keys[path] = nil }
}

@MainActor
@Test func trustingTheNewKeyForgetsTheOldOneAndPinsWhatAnswersNext() async {
    // What the panel's one way out does. The rotation was real, so the key on
    // that socket becomes the key this application knows it by — and it is
    // written down, or the same panel appears again on the next connection.
    let transport = FakeTransport([
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n"]),
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n"]),
    ])
    let keys = RecordingKeys()
    keys.pin("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=", forSocket: Proven.socket)
    let supervisor = FeedSupervisor(
        transport: transport, socketPath: Proven.socket, keys: keys,
        nonces: FixedNonce(Proven.nonce))

    supervisor.start()
    await eventually("the refusal") {
        if case .unproven = supervisor.state { return true } else { return false }
    }

    supervisor.forgetPinnedKey()

    await eventually("the link to come up on the new key") { supervisor.state.isLive }
    #expect(keys.pinned(forSocket: Proven.socket) == Proven.key)
}

@MainActor
@Test func forgettingThePinnedKeyDoesNotTrustTheKeyThatWasOffered() async {
    // Forgetting is not accepting: the next publisher still has to sign the
    // challenge before anything it says is shown. A relay that got somebody to
    // click the button would otherwise be through.
    let transport = FakeTransport([
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n"]),
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.wrongAuth + "\n"]),
    ])
    let keys = RecordingKeys()
    keys.pin("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=", forSocket: Proven.socket)
    let supervisor = FeedSupervisor(
        transport: transport, socketPath: Proven.socket, keys: keys,
        nonces: FixedNonce(Proven.nonce))

    supervisor.start()
    await eventually("the refusal") {
        if case .unproven = supervisor.state { return true } else { return false }
    }
    supervisor.forgetPinnedKey()

    await eventually("the second refusal") {
        if case .unproven = supervisor.state, transport.attempts == 2 { return true }
        return false
    }
    #expect(keys.pinned(forSocket: Proven.socket) == nil)
}

@MainActor
@Test func theTailOfAnAbandonedConnectionCannotKnockDownTheOneThatReplacedIt() async {
    // A real close returns before the stream it feeds has finished: DispatchIO
    // calls its read handler on another queue, so the end of a connection this
    // program has already walked away from lands whenever it lands - which can
    // be after the next connection is up and being read.
    //
    // Delivered to the machine, it reads as "the link just dropped", and the
    // hello of the connection that is actually open arrives in a state that
    // ignores hellos. That is a menu bar stuck on a refusal that has been
    // dealt with, and it took restarting the application to clear.
    let transport = FakeTransport([
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n"]),
        .deliverAndStayOpen([Fixtures.hello + "\n" + Fixtures.auth + "\n"]),
    ])
    let keys = RecordingKeys()
    keys.pin("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=", forSocket: Proven.socket)
    let supervisor = FeedSupervisor(
        transport: transport, socketPath: Proven.socket, keys: keys,
        nonces: FixedNonce(Proven.nonce))

    supervisor.start()
    await eventually("the refusal") {
        if case .unproven = supervisor.state { return true } else { return false }
    }
    supervisor.forgetPinnedKey()
    await eventually("the link to come up again") { supervisor.state.isLive }

    // The first connection, closed by the refusal, finally finishes.
    transport.endStream(0)

    await eventually("nothing at all to happen") { transport.attempts == 2 }
    #expect(supervisor.state.isLive)
}

@MainActor
@Test func whatAnAbandonedConnectionSaysOnItsWayOutIsNotAboutTheNextOne() async {
    // A connect still in flight for a link nobody is waiting for any more. Its
    // failure used to be handed to the machine anyway, which read it as the
    // connection being opened right now having failed - and scheduled a retry
    // over the top of it. On the real transport this ran forever: every fresh
    // hello arrived in a state that ignores hellos, and only restarting the
    // application cleared it.
    let transport = FakeTransport([
        .stallsThenRefuses(ECONNREFUSED),
        .stallsThenRefuses(ECONNREFUSED),
    ])
    let supervisor = FeedSupervisor(transport: transport, keys: NoPinnedKeys())

    supervisor.start()
    await eventually("the first attempt to be made") { transport.attempts == 1 }
    supervisor.stop()
    supervisor.start()
    await eventually("the second attempt to be made") { transport.attempts == 2 }

    transport.release(0)

    // Nothing to wait for: the point is that nothing happens. A moment is
    // enough for a dispatch that was going to be made.
    try? await Task.sleep(for: .milliseconds(50))
    guard case .connecting = supervisor.state else {
        Issue.record("state = \(supervisor.state), want it still connecting")
        return
    }
}

@MainActor
@Test func aReadThatFailsIsNotAConnectThatFailed() async {
    // DispatchIO reports a failed read as the same error type a failed connect
    // throws. Told apart by the type alone, this program closed a connection,
    // caught its own ECANCELED and announced "cannot reach tun-manager (error
    // 89)" about a socket it had been reading a moment earlier.
    let transport = FakeTransport([
        .deliverThenFailToRead([Fixtures.hello + "\n" + Fixtures.auth + "\n"], ECANCELED)
    ])
    let supervisor = proving(transport)

    supervisor.start()

    await eventually("the link to give up") {
        if case .retrying = supervisor.state { return true } else { return false }
    }
    guard case .retrying(let because) = supervisor.state else { return }
    #expect(because == .lost)
}
