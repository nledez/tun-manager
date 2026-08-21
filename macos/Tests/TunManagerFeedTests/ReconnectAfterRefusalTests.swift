import Foundation
import Testing

@testable import TunManagerFeed

/// Serialised for the reason UnixSocketTransportTests is: these open real
/// descriptors, and a suite running a hundred tests in parallel trips
/// libdispatch's own assertion about a descriptor going away under an active
/// channel. A property of the harness, not of the program - in use there is one
/// connection at a time, opened and closed by the supervisor on the main actor.
extension RealSockets {
@Suite struct Reconnect {

/// The one path a scripted transport cannot show: a real socket, closed for
/// real, dialled again.
@MainActor
@Test func trustingTheNewKeyOpensARealConnectionAgain() async throws {
    let publisher = try LocalPublisher()
    defer { publisher.stop() }

    let keys = MemoryKeys()
    keys.pin("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=", forSocket: publisher.path)
    let supervisor = FeedSupervisor(
        // Not root, like the demo publisher it stands in for: this test is
        // about the proof exchange, and the credentials rule has its own tests.
        transport: UnixSocketTransport(
            path: publisher.path, policy: PeerPolicy(requiresRoot: false)),
        socketPath: publisher.path,
        keys: keys)

    supervisor.start()
    await eventually("the refusal", timeout: .seconds(6)) {
        if case .unproven = supervisor.state { return true } else { return false }
    }

    supervisor.forgetPinnedKey()

    await eventually("the link to come up on the key that answered", timeout: .seconds(6)) {
        supervisor.state.isLive
    }
    #expect(publisher.accepted == 2)
    #expect(keys.pinned(forSocket: publisher.path) == publisher.key)
}

}
}
