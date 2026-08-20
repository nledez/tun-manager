import Foundation
import Testing

@testable import TunManagerFeed

/// The one path a scripted transport cannot show: a real socket, closed for
/// real, dialled again.
@MainActor
@Test func trustingTheNewKeyOpensARealConnectionAgain() async throws {
    let publisher = try LocalPublisher()
    defer { publisher.stop() }

    let keys = MemoryKeys()
    keys.pin("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=", forSocket: publisher.path)
    let supervisor = FeedSupervisor(
        transport: UnixSocketTransport(path: publisher.path), socketPath: publisher.path,
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

final class MemoryKeys: PinnedKeys, @unchecked Sendable {
    private let lock = NSLock()
    private var keys: [String: String] = [:]

    func pinned(forSocket path: String) -> String? {
        lock.lock()
        defer { lock.unlock() }
        return keys[path]
    }
    func pin(_ key: String, forSocket path: String) {
        lock.lock()
        keys[path] = key
        lock.unlock()
    }
    func forget(socket path: String) {
        lock.lock()
        keys[path] = nil
        lock.unlock()
    }
}
