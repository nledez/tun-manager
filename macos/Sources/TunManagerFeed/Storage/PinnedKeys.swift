import Foundation
import Security

/// Where the key each socket's publisher is known by is kept.
///
/// A protocol because it is the one piece of this that touches something
/// outside the process, and because a test that wrote to the real keychain
/// would be a test that changes the machine it runs on.
public protocol PinnedKeys: Sendable {
    /// The key pinned for this socket, or nil the first time.
    func pinned(forSocket path: String) -> String?
    /// Remember this key for this socket, replacing whatever was there.
    func pin(_ key: String, forSocket path: String)
    /// Forget it, so the next connection pins afresh.
    func forget(socket path: String)
}

/// NOT TESTED: the calls into Security.framework. A test that exercised them
/// would write into the keychain of whoever ran the suite, and read back what a
/// previous run left there; one that ran in CI would find no keychain at all.
/// What decides anything — when to pin, what to compare, what to do when they
/// differ — is in LinkMachine, against a store in memory.
/// See macos/docs/coverage-gaps.md, "the keychain".
///
/// The keychain, which is where this belongs.
///
/// Not UserDefaults, which was the obvious alternative and is the wrong one:
/// any process running as this user can write a defaults key, and the whole
/// point of pinning is to notice when the thing on the socket is not the one
/// that was there before. A pin somebody else can rewrite is a pin that agrees
/// with them.
///
/// Keyed by socket path, so a demo publisher on /tmp is pinned separately from
/// the real one on /var/run and nothing learnt from one touches the other.
public struct KeychainPinnedKeys: PinnedKeys {
    /// The keychain service every entry sits under.
    static let service = "net.ledez.tun-manager.feed-key"

    public init() {}

    public func pinned(forSocket path: String) -> String? {
        var query = Self.query(path)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
            let data = item as? Data
        else {
            return nil
        }
        return String(data: data, encoding: .utf8)
    }

    public func pin(_ key: String, forSocket path: String) {
        forget(socket: path)

        var item = Self.query(path)
        item[kSecValueData as String] = Data(key.utf8)
        // This device only, and not while it is locked: the pin is about a unix
        // socket on this machine, and it has no meaning anywhere else.
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(item as CFDictionary, nil)
    }

    public func forget(socket path: String) {
        SecItemDelete(Self.query(path) as CFDictionary)
    }

    private static func query(_ path: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: path,
        ]
    }
}

/// A store that remembers nothing, for a run that should pin nothing.
public struct NoPinnedKeys: PinnedKeys {
    public init() {}
    public func pinned(forSocket path: String) -> String? { nil }
    public func pin(_ key: String, forSocket path: String) {}
    public func forget(socket path: String) {}
}
