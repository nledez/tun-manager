import CryptoKit
import Foundation

/// Whether the thing at the end of the socket holds the key it claims to.
///
/// The same message tun-manager signs, rebuilt here: the name of the protocol,
/// the schema, the version, the socket path and the nonce, separated by nul
/// bytes. Rebuilt rather than taken from the answer — a signature is only worth
/// what the verifier decided to check, and a verifier that signed off on
/// whatever bytes it was handed would be checking nothing.
public enum PublisherProof {
    /// The first field of everything this key signs, so a signature made for
    /// this protocol cannot be presented as one made for another.
    static let domain = "tun-manager-feed-v1"

    /// A nul byte, because it appears in none of the other fields: without a
    /// separator, a version ending in what the path begins with would produce
    /// the same bytes as a shorter version and a longer path.
    static let separator: UInt8 = 0

    /// What the publisher signed, as this client believes it.
    ///
    /// The path is the one this client dialled, not one the publisher claimed:
    /// something listening elsewhere can forward the challenge to the real
    /// tun-manager and hand back a genuine signature, and this is what makes
    /// that answer fail here.
    public static func signedMessage(schema: Int, version: String, path: String, nonce: Data) -> Data
    {
        var message = Data(domain.utf8)
        for field in [Data(String(schema).utf8), Data(version.utf8), Data(path.utf8), nonce] {
            message.append(separator)
            message.append(field)
        }
        return message
    }

    /// Whether the signature is one this key made over that message.
    ///
    /// False for everything that is not a proof: a key that is not one, a
    /// signature that is not one, and a signature over something else. There is
    /// no third answer — "cannot tell" and "no" lead to the same place, and a
    /// caller with two of them to handle is a caller that will get one wrong.
    public static func holds(
        key: String, signature: String, nonce: Data, schema: Int, version: String, path: String
    ) -> Bool {
        guard let keyBytes = Data(base64Encoded: key),
            let signatureBytes = Data(base64Encoded: signature),
            let publicKey = try? Curve25519.Signing.PublicKey(rawRepresentation: keyBytes)
        else {
            return false
        }
        return publicKey.isValidSignature(
            signatureBytes,
            for: signedMessage(schema: schema, version: version, path: path, nonce: nonce))
    }
}
