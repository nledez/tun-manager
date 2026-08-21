import Foundation

@testable import TunManagerFeed

/// A pinned-key store in memory, so no test ever writes into the keychain of
/// whoever runs the suite.
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
