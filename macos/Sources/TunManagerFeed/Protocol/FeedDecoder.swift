import Foundation
import os

/// Turns one line from the feed into a message.
///
/// The coding keys below are the wire contract, written out rather than derived
/// from a key strategy, so a reviewer can diff them line for line against the
/// struct tags in internal/wire/wire.go. A strategy would make the contract
/// implicit and let a rename drift silently.
public enum FeedDecoder {
    private static let log = Logger(subsystem: "net.ledez.tun-manager", category: "feed")

    /// Built per call, not shared.
    ///
    /// A JSONDecoder carries mutable state through a decode and is not safe to
    /// use from two threads at once; a single shared instance corrupts its own
    /// heap, and the crash surfaces somewhere else entirely — this was found as
    /// intermittent failures inside Foundation's scanner, with nothing pointing
    /// back here. Building one costs an allocation against a line that arrives
    /// every few minutes.
    private static func makeDecoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let text = try container.decode(String.self)
            guard let date = RFC3339.date(from: text) else {
                throw DecodingError.dataCorruptedError(
                    in: container, debugDescription: "not an RFC 3339 timestamp: \(text)")
            }
            return date
        }
        return decoder
    }

    /// Decodes one line, or returns nil for a line this client has nothing to
    /// do with.
    ///
    /// It does not throw, and that is deliberate: there is nothing a caller
    /// could usefully do. Dropping the connection over a line we chose not to
    /// understand is how a client breaks itself against a publisher that only
    /// added a field.
    public static func decode(_ line: Data) -> FeedMessage? {
        let decoder = makeDecoder()
        guard let envelope = try? decoder.decode(Envelope.self, from: line) else {
            return nil
        }

        do {
            switch envelope.type {
            case "hello":
                let wire = try decoder.decode(WireHello.self, from: line)
                return .hello(schema: wire.schema, version: wire.version, publicKey: wire.pubkey)
            case "auth":
                let wire = try decoder.decode(WireAuth.self, from: line)
                return .auth(nonce: wire.nonce, signature: wire.signature)
            case "state":
                return .state(try decoder.decode(WireState.self, from: line).snapshot)
            case "sample":
                return .sample(try decoder.decode(WireSample.self, from: line).sample)
            case "ping":
                return .ping(try decoder.decode(WirePing.self, from: line).pings)
            case "bye":
                return .bye
            default:
                // A type this client has never heard of. Silence is the whole
                // point: it is what lets the publisher add one.
                return nil
            }
        } catch {
            // The one case worth saying out loud: both sides claim to
            // understand this message and they disagree about its shape.
            log.error("feed: \(envelope.type, privacy: .public) line did not decode: \(error)")
            return nil
        }
    }
}

// MARK: - The wire

private struct Envelope: Decodable {
    let type: String
}

private struct WireHello: Decodable {
    let schema: Int
    let version: String
    /// The public half of the key tun-manager is known by, base64. Absent from
    /// a publisher that has no key, and from every publisher older than the
    /// version that started sending one.
    let pubkey: String?
}

private struct WireAuth: Decodable {
    let nonce: String
    let signature: String
}

private struct WireState: Decodable {
    let context: WireContext
    let taken: Date
    /// Not optional on purpose. The publisher builds this slice explicitly so
    /// it marshals as [] and never as null, so a null is a broken line — and
    /// rendering "no tunnels" for a broken line is the worst failure this
    /// program has available.
    let tunnels: [WireTunnel]

    var snapshot: Snapshot {
        Snapshot(context: context.value, taken: taken, tunnels: tunnels.map(\.value))
    }
}

private struct WireContext: Decodable {
    let name: String
    let interface: String?
    let address: String?

    var value: FeedContext { FeedContext(name: name, interface: interface, address: address) }
}

private struct WireTunnel: Decodable {
    let name: String
    let group: String
    let health: String
    let device: String?
    let endpoint: String?
    let checkIP: String?
    let lastHandshake: Date?
    let rxBytes: Int64
    let txBytes: Int64

    private enum CodingKeys: String, CodingKey {
        case name, group, health, device, endpoint
        case checkIP = "check_ip"
        case lastHandshake = "last_handshake"
        case rxBytes = "rx_bytes"
        case txBytes = "tx_bytes"
    }

    var value: TunnelStatus {
        TunnelStatus(
            name: name, group: group, health: Health(wire: health),
            device: device, endpoint: endpoint, checkIP: checkIP,
            lastHandshake: lastHandshake, rxBytes: rxBytes, txBytes: txBytes)
    }
}

private struct WirePing: Decodable {
    /// Not optional, for the same reason a state line's tunnels are not: the
    /// publisher builds this slice explicitly so it marshals as [] rather than
    /// null, which makes a null a broken line rather than an empty round.
    let results: [WirePingResult]

    var pings: [Ping] { results.map(\.value) }
}

private struct WirePingResult: Decodable {
    let tunnel: String
    /// Milliseconds, and absent when the probe failed.
    let rttMs: Double?
    let error: String?

    private enum CodingKeys: String, CodingKey {
        case tunnel, error
        case rttMs = "rtt_ms"
    }

    var value: Ping {
        Ping(
            tunnel: tunnel,
            rtt: rttMs.map { .milliseconds($0) },
            error: error)
    }
}

private struct WireSample: Decodable {
    let tunnel: String
    let at: Date
    let rx: Int64
    let tx: Int64

    var sample: Sample { Sample(tunnel: tunnel, at: at, rx: rx, tx: tx) }
}
