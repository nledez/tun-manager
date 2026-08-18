import Foundation

/// The verbs this client may send.
///
/// The publisher accepts these three and no others, and none of them can start
/// or stop a tunnel — which is the whole reason the socket needs no
/// authorisation.
public enum ClientCommand: Sendable, Equatable {
    /// Asks for a fresh view. The publisher accepts at most one every two
    /// seconds and acknowledges none of them: a refresh is observed as the
    /// `state` line that follows, or not at all.
    case refresh
    /// Starts a reading of this tunnel's counters once a second, for as long as
    /// somebody is looking. A tunnel the publisher has never heard of is
    /// ignored, silently.
    case watch(String)
    case unwatch(String)

    public var line: Data {
        // Encoded rather than interpolated. Tunnel names come from file names
        // on somebody else's disk, and pasting one into a JSON literal is how
        // a name containing a quote becomes a broken line — or a second field.
        let encoder = JSONEncoder()
        guard let body = try? encoder.encode(Wire(self)) else {
            // Encoding a string into a two-field object has no failure mode
            // reachable from here.
            return Data()
        }
        return body + Data([UInt8(ascii: "\n")])
    }
}

/// The wire shape, written out so the field order is the publisher's rather
/// than whatever a synthesised encoder chooses.
private struct Wire: Encodable {
    let type: String
    let tunnel: String?

    init(_ command: ClientCommand) {
        switch command {
        case .refresh:
            (type, tunnel) = ("refresh", nil)
        case .watch(let name):
            (type, tunnel) = ("watch", name)
        case .unwatch(let name):
            (type, tunnel) = ("unwatch", name)
        }
    }

    private enum CodingKeys: String, CodingKey {
        case type, tunnel
    }

    func encode(to encoder: any Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(type, forKey: .type)
        try container.encodeIfPresent(tunnel, forKey: .tunnel)
    }
}
