import Foundation

@testable import TunManagerFeed

/// Wire lines as the publisher writes them. Invented throughout: the tunnel
/// names are the repository's placeholders and the addresses come from the
/// ranges reserved for documentation (RFC 5737), so no fixture can name a real
/// host.
enum Fixtures {
    /// A hello from a publisher speaking what this build understands, with the
    /// key it is known by. The pubkey is the public half of the seed the Go
    /// tests use, so both sides are talking about the same key.
    static let hello =
        #"{"type":"hello","schema":2,"version":"v0.6.0","pubkey":"A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}"#

    /// The answer to a challenge of Proven.nonce, made by the key that hello
    /// announces. Taken from internal/feed, like the rest of it.
    static let auth =
        #"{"type":"auth","nonce":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=","signature":"N0zb6kCOKreEzkejmsMp94YoDsz4nHcX40btcVOQFbma1sxBjNWWszuHJfz8dyAWTGDNxSi2uHjqComOT0HaDA=="}"#

    /// The same answer with one bit of the signature flipped: a publisher that
    /// echoes the nonce back and cannot sign it. Ed25519 has no near misses, so
    /// this is what everything short of the real key looks like.
    static let wrongAuth =
        #"{"type":"auth","nonce":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=","signature":"Nkzb6kCOKreEzkejmsMp94YoDsz4nHcX40btcVOQFbma1sxBjNWWszuHJfz8dyAWTGDNxSi2uHjqComOT0HaDA=="}"#

    /// A full view: one tunnel up with everything filled in, one down with
    /// every optional key omitted.
    static let state = """
        {"type":"state","context":{"name":"office","interface":"en0","address":"198.51.100.42"},\
        "taken":"2026-08-17T14:03:11.123456789+02:00","tunnels":[\
        {"name":"alpha","group":"needed","health":"up","device":"utun7",\
        "endpoint":"192.0.2.10:51820","check_ip":"10.20.30.1",\
        "last_handshake":"2026-08-17T14:02:58+02:00","rx_bytes":184320,"tx_bytes":92160},\
        {"name":"bravo","group":"","health":"down","endpoint":"bravo.example:51820",\
        "rx_bytes":0,"tx_bytes":0}]}
        """

    static let emptyState = """
        {"type":"state","context":{"name":""},\
        "taken":"2026-08-17T14:03:11.123456789+02:00","tunnels":[]}
        """

    /// A round of probes: one that answered, one that did not.
    static let ping = """
        {"type":"ping","results":[{"tunnel":"alpha","rtt_ms":18.4},\
        {"tunnel":"bravo","error":"timeout"}]}
        """

    static let bye = #"{"type":"bye"}"#

    static func line(_ text: String) -> Data { Data(text.utf8) }
}

/// One publisher, proved. The key, the nonce and the signature come from
/// internal/feed on the Go side, so a machine taken through this exchange has
/// been through the real one rather than a rehearsal of it.
enum Proven {
    static let socket = "/var/run/tun-manager.sock"
    static let version = "v0.6.0"
    static let key = "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="
    static let nonce = Data(0..<32)
    static let signature =
        "N0zb6kCOKreEzkejmsMp94YoDsz4nHcX40btcVOQFbma1sxBjNWWszuHJfz8dyAWTGDNxSi2uHjqComOT0HaDA=="

    /// A machine that draws the nonce that signature answers.
    static func machine(pinned: String? = nil) -> LinkMachine {
        LinkMachine(socketPath: socket, nonces: FixedNonce(nonce), pinnedKey: pinned)
    }

    /// Everything the publisher says on the way to being believed, as one step:
    /// the hello, and the answer to the challenge it provokes.
    static func greet(_ machine: inout LinkMachine, version: String = version) -> [LinkAction] {
        var actions = machine.handle(
            .message(.hello(schema: LinkMachine.schema, version: version, publicKey: key)))
        actions += machine.handle(
            .message(.auth(nonce: nonce.base64EncodedString(), signature: signature)))
        return actions
    }
}

/// A nonce a test chooses, because the machine has to be deterministic to be
/// tested and randomness is the one thing in it that cannot be.
struct FixedNonce: NonceSource {
    let value: Data
    init(_ value: Data) { self.value = value }
    func nonce() -> Data { value }
}
