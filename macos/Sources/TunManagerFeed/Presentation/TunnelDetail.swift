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

    /// - Parameter latest: the most recent reading of this tunnel's counters,
    ///   if it is being watched. The view's own counters are minutes old by the
    ///   time anybody reads them, and showing those beside a chart drawn from
    ///   fresher ones is the window disagreeing with itself.
    public init(
        _ tunnel: TunnelStatus, latest: Sample? = nil, now: Date,
        locale: Locale = .autoupdatingCurrent
    ) {
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
            // Two tunnels can be watched at once, so a reading is only taken
            // when it is this tunnel's.
            let reading = latest?.tunnel == tunnel.name ? latest : nil
            facts.append(
                Fact(
                    label: "Received",
                    value: Formatting.bytes(reading?.rx ?? tunnel.rxBytes, locale: locale)))
            facts.append(
                Fact(
                    label: "Sent",
                    value: Formatting.bytes(reading?.tx ?? tunnel.txBytes, locale: locale)))
        }
        if let check = tunnel.checkIP {
            facts.append(Fact(label: "Checks", value: check))
        }
        self.facts = facts
    }
}
