import Foundation
import Security

/// Where the bytes a publisher is asked to sign come from.
///
/// A protocol because the machine has to be deterministic to be tested, and
/// randomness is the one thing in it that cannot be. Everything else about a
/// connection is decided from what arrived.
public protocol NonceSource: Sendable {
    /// Thirty-two bytes, fresh. A nonce reused across two connections is a
    /// question somebody already has the answer to.
    func nonce() -> Data
}

/// The system's randomness, which is what the application uses.
public struct SystemNonces: NonceSource {
    /// The size tun-manager insists on. A shorter one is refused there rather
    /// than signed.
    public static let length = 32

    public init() {}

    public func nonce() -> Data {
        var bytes = [UInt8](repeating: 0, count: Self.length)
        if SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) != errSecSuccess {
            // SecRandomCopyBytes does not fail on a working system. If it ever
            // does, a nonce of zeroes would be a question with a known answer,
            // so this asks an unanswerable one instead: the signature will not
            // verify and the link will not come up, which is the safe way to be
            // wrong.
            return Data(repeating: 0, count: Self.length) + Data("no randomness".utf8)
        }
        return Data(bytes)
    }
}
