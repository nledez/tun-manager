import Foundation
import Testing

@testable import TunManagerFeed

/// A view with tunnels in it, sent by every impostor below before it is asked
/// to prove anything. It is the thing an attacker is here to have drawn.
private let aViewWorthShowing = """
    {"type":"state","context":{"name":"office"},\
    "taken":"2026-08-17T14:03:11.123456789+02:00","tunnels":[\
    {"name":"alpha","group":"needed","health":"up","rx_bytes":1,"tx_bytes":1}]}
    """

/// What the menu would draw from a supervisor, which is the only place the
/// question "did any of it get through" can honestly be asked.
@MainActor
private func displayed(_ supervisor: FeedSupervisor, socket: String) -> MenuModel {
    MenuModelBuilder.build(
        state: supervisor.state, snapshot: supervisor.snapshot, now: Date(), socketPath: socket)
}

/// Counts what the application was told to show, notify about and chart.
private final class Spy: FeedObserver, @unchecked Sendable {
    private let lock = NSLock()
    private var published = 0

    var publishes: Int {
        lock.lock()
        defer { lock.unlock() }
        return published
    }

    func linkDidChange(state: LinkState, snapshot: Snapshot?, publisherVersion: String?) {}
    func linkDidPublish(snapshot: Snapshot, diff: SnapshotDiff) {
        lock.lock()
        published += 1
        lock.unlock()
    }
    func linkDidSample(_ sample: Sample) {}
}

@MainActor
private func hostile(
    _ publisher: LocalPublisher, pinned: String? = nil, keys: MemoryKeys = MemoryKeys()
) -> (FeedSupervisor, Spy) {
    if let pinned { keys.pin(pinned, forSocket: publisher.path) }
    let supervisor = FeedSupervisor(
        transport: UnixSocketTransport(
            path: publisher.path, policy: PeerPolicy(requiresRoot: false)),
        socketPath: publisher.path, keys: keys)
    let spy = Spy()
    supervisor.observer = spy
    return (supervisor, spy)
}

@MainActor
private func refused(_ supervisor: FeedSupervisor) async -> Bool {
    await eventually("the refusal", timeout: .seconds(6)) {
        switch supervisor.state {
        case .unproven, .blocked: return true
        default: return false
        }
    }
    switch supervisor.state {
    case .unproven, .blocked: return true
    default: return false
    }
}

extension RealSockets {
    @Suite @MainActor struct Attacks {

        @Test func aPublisherWithItsOwnKeyIsRefusedOnceOneHasBeenPinned() async throws {
            // The plain case: something else is listening where tun-manager
            // was, and it holds a key of its own. Its signature is perfectly
            // valid — for the wrong key.
            let attacker = try LocalPublisher(sayingFirst: aViewWorthShowing)
            defer { attacker.stop() }
            let (supervisor, spy) = hostile(
                attacker, pinned: "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")

            supervisor.start()

            #expect(await refused(supervisor))
            #expect(spy.publishes == 0)
        }

        @Test func aCapturedAnswerIsGoodOnceAndOnlyForTheConnectionItAnswered() async throws {
            // Somebody recording that socket has a genuine (nonce, signature)
            // pair made by the pinned key. Replaying it works only if the
            // question is asked twice, and the client draws a new nonce for
            // every connection precisely so that it never is.
            let publisher = try LocalPublisher()
            defer { publisher.stop() }
            let (supervisor, _) = hostile(publisher)

            supervisor.start()
            await eventually("the honest connection") { supervisor.state.isLive }
            let captured = try #require(publisher.lastAuth)

            publisher.behaviour = .replaying(nonce: captured.nonce, signature: captured.signature)
            supervisor.stop()
            supervisor.start()

            #expect(await refused(supervisor))
        }

        @Test func aRelayCannotPassOffTheAnswerItWasGiven() async throws {
            // The attack this defeats: something listens on one socket,
            // forwards every challenge to the real publisher, and hands back
            // the genuine signature it gets. The signature is real, the key is
            // the real one — and it covers the path the real publisher is
            // listening on, not the one this client dialled.
            //
            // Nothing is pinned here on purpose: on a first connection the
            // signature verifies against the key it was shown, so the path is
            // the only thing standing between a relay and being believed.
            let relay = try LocalPublisher(
                behaviour: .relaying(path: "/var/run/tun-manager.sock"),
                sayingFirst: aViewWorthShowing)
            defer { relay.stop() }
            let keys = MemoryKeys()
            let (supervisor, spy) = hostile(relay, keys: keys)

            supervisor.start()

            #expect(await refused(supervisor))
            #expect(keys.pinned(forSocket: relay.path) == nil)
            #expect(spy.publishes == 0)
        }

        @Test func aPublisherThatSpeaksAnOlderSchemaIsNotHumoured() async throws {
            // Schema 1 had no proof in it. Accepting it would mean accepting a
            // publisher that cannot be asked to prove anything, which is what
            // something standing in for tun-manager would rather be taken for.
            let old = try LocalPublisher(schema: 1, sayingFirst: aViewWorthShowing)
            defer { old.stop() }
            let (supervisor, spy) = hostile(old)

            supervisor.start()

            #expect(await refused(supervisor))
            #expect(supervisor.state == LinkState.blocked(theirSchema: 1))
            #expect(spy.publishes == 0)
        }

        @Test func aPublisherThatNeverAnswersIsGivenUpOnRatherThanWaitedFor() async throws {
            // It accepts the connection, sends a view, and says nothing else.
            // Without a deadline the link would sit there forever, connected,
            // showing what it was sent.
            let mute = try LocalPublisher(behaviour: .silent, sayingFirst: aViewWorthShowing)
            defer { mute.stop() }
            let (supervisor, spy) = hostile(mute)

            supervisor.start()

            #expect(await refused(supervisor))
            #expect(spy.publishes == 0)
        }

        @Test func afterARefusalNothingItSentHasReachedWhatIsOnScreen() async throws {
            // The property the rest of this file is machinery for. Every
            // impostor above sends a view before it is asked to prove anything,
            // because that is the first thing a menu would draw — and none of
            // it is anywhere the menu can reach it.
            let attacker = try LocalPublisher(
                behaviour: .silent, sayingFirst: aViewWorthShowing)
            defer { attacker.stop() }
            let (supervisor, spy) = hostile(
                attacker, pinned: "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")

            supervisor.start()
            #expect(await refused(supervisor))

            let menu = displayed(supervisor, socket: attacker.path)
            #expect(supervisor.snapshot == nil)
            #expect(menu.sections.isEmpty)
            #expect(menu.showsOverview == false)
            #expect(menu.canRefresh == false)
            #expect(menu.warning != nil)
            #expect(menu.footnote == "Nothing known yet")
            #expect(spy.publishes == 0)
            #expect(supervisor.pings.isEmpty)
        }
    }
}
