import CryptoKit
import Foundation

/// How a feed key is shown to a person.
///
/// The same rendering tun-manager prints: the first sixteen bytes of the
/// SHA-256 of the public half, in hex pairs. Two programs computing it
/// separately is the point — somebody compares what this window says against
/// what `sudo tun-manager feed-key` said, and neither of them is the key.
public enum Fingerprint {
    /// How many bytes of the digest are shown. Sixteen is what ssh settled on
    /// for the same job: long enough that two keys will not collide in any
    /// population this program will see, short enough to compare by eye.
    static let length = 16

    /// The fingerprint of a key already decoded.
    public static func of(_ key: Data) -> String {
        SHA256.hash(data: key)
            .prefix(length)
            .map { String(format: "%02x", $0) }
            .joined(separator: ":")
    }

    /// The fingerprint of the key as it arrives in the hello, or nil for
    /// anything that is not one.
    ///
    /// Nil rather than a fingerprint of whatever the bytes happened to be: a
    /// truncated key shown as a fingerprint invites somebody to compare it with
    /// tun-manager's and conclude the publisher has changed.
    public static func of(base64 encoded: String) -> String? {
        guard let key = Data(base64Encoded: encoded), key.count == keyLength else { return nil }
        return of(key)
    }

    /// An Ed25519 public key is thirty-two bytes, and nothing else is one.
    private static let keyLength = 32
}
