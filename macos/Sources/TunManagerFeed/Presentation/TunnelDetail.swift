import Foundation

/// One tunnel, laid out for the detail window.
///
/// The same rule as the menu: what to show, and what to leave out, is decided
/// here where a test can reach it. The window draws the result and makes no
/// choices of its own.
public struct TunnelDetail: Sendable, Equatable {
    /// A labelled row.
    public struct Fact: Sendable, Equatable {
        public let label: String
        public let value: String
    }

    public let name: String
    public let health: Health
    /// Never blank: a tunnel in no group says so, because an empty line under a
    /// heading reads as a missing value rather than as an absent one.
    public let group: String
    public let facts: [Fact]

    public init(_ tunnel: TunnelStatus, now: Date, locale: Locale = .autoupdatingCurrent) {
        name = tunnel.name
        health = tunnel.health
        group = tunnel.group.isEmpty ? "no group" : tunnel.group

        var facts: [Fact] = []
        if let device = tunnel.device {
            facts.append(Fact(label: "Interface", value: device))
        }
        if let endpoint = tunnel.endpoint {
            facts.append(Fact(label: "Endpoint", value: endpoint))
        }
        if let handshake = tunnel.lastHandshake {
            facts.append(
                Fact(
                    label: "Handshake",
                    value: "\(Formatting.age(now.timeIntervalSince(handshake))) ago"))
        }
        // Only for a tunnel carrying something. The counters are always on the
        // wire, zero included, and a pair of zeroes under a down tunnel is
        // noise dressed as a measurement.
        if tunnel.health != .down {
            facts.append(
                Fact(label: "Received", value: Formatting.bytes(tunnel.rxBytes, locale: locale)))
            facts.append(Fact(label: "Sent", value: Formatting.bytes(tunnel.txBytes, locale: locale)))
        }
        if let check = tunnel.checkIP {
            facts.append(Fact(label: "Checks", value: check))
        }
        self.facts = facts
    }
}
