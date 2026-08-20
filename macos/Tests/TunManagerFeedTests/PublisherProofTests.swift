import Foundation
import Testing

@testable import TunManagerFeed

// Everything below was produced by internal/feed on the Go side: the key, the
// nonce, and the signature over schema 2, version v0.6.0 and the socket path.
// That is the point of pinning them here — two implementations of one
// agreement, and a test that fails the day they stop making it.
private let key = "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="
private let nonce = Data(0..<32)
private let signature = "N0zb6kCOKreEzkejmsMp94YoDsz4nHcX40btcVOQFbma1sxBjNWWszuHJfz8dyAWTGDNxSi2uHjqComOT0HaDA=="
private let path = "/var/run/tun-manager.sock"

private func proof(
    key: String = key, signature: String = signature, nonce: Data = nonce,
    schema: Int = 2, version: String = "v0.6.0", path: String = path
) -> Bool {
    PublisherProof.holds(
        key: key, signature: signature, nonce: nonce, schema: schema, version: version, path: path)
}

@Test func aSignatureFromTunManagerVerifies() {
    #expect(proof())
}

@Test func aSignatureOverAnotherNonceDoesNot() {
    // The nonce is this client's, drawn for this connection. An answer to
    // somebody else's question is an answer somebody kept.
    #expect(proof(nonce: Data(repeating: 9, count: 32)) == false)
}

@Test func aSignatureFromAnotherSocketDoesNot() {
    // What stops a relay: it can forward the question to the real publisher and
    // hand back a genuine answer, but the answer names the socket it came from,
    // and this client dialled a different one.
    #expect(proof(path: "/tmp/somebody-elses.sock") == false)
}

@Test func aSignatureFromAnotherVersionOrSchemaDoesNot() {
    #expect(proof(schema: 3) == false)
    #expect(proof(version: "v0.7.0") == false)
}

@Test func anotherKeyDoesNotVerifyIt() {
    // The pinned key is the whole question: this is what a publisher that is
    // not the pinned one looks like.
    #expect(proof(key: "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=") == false)
}

@Test func nothingThatIsNotASignatureVerifies() {
    #expect(proof(signature: "not base64 at all!") == false)
    #expect(proof(signature: Data([1, 2, 3]).base64EncodedString()) == false)
}

@Test func nothingThatIsNotAKeyVerifies() {
    #expect(proof(key: "not base64 at all!") == false)
    #expect(proof(key: Data([1, 2, 3]).base64EncodedString()) == false)
}
