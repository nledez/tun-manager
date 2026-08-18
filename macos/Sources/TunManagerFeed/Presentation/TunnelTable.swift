import Foundation

/// One tunnel as a row of the table — the same four columns the terminal
/// shows: handshake, traffic, ping, endpoint.
///
/// Every cell is a finished string. What to show, and what to leave blank, is
/// decided here where a test can reach it; the window draws the result and
/// makes no choices of its own.
public struct TunnelRow: Sendable, Equatable, Identifiable {
    /// The latency cell. An enum rather than a string because a failed probe is
    /// drawn differently from a slow one, and the reason is worth keeping for
    /// whoever hovers it.
    public enum PingCell: Sendable, Equatable {
        /// Not probed, or nothing to probe.
        case none
        case rtt(String)
        case failed(reason: String)
    }

    public let name: String
    /// The name. A tunnel's name is its identity everywhere else in this
    /// program — it is what the publisher keys watches and probes by — so a
    /// separate one here would be a second identity to keep in step.
    public var id: String { name }
    public let health: Health
    /// Never blank: a tunnel in no group says so, because an empty cell under a
    /// heading reads as a missing value rather than as an absent one.
    public let group: String
    /// Blank for a tunnel that is down, throughout. A down tunnel has no
    /// interface, so it has no handshake, no counters and no latency — and a
    /// placeholder in those cells reads like data.
    public let handshake: String
    public let traffic: String
    public let ping: PingCell
    /// From the configuration, so it is shown whatever the state.
    public let endpoint: String
}

public enum TunnelTable {
    /// Builds a row per tunnel, in the order the publisher sent them — which is
    /// the order the terminal lists them in, so the two read alike.
    ///
    /// - Parameters:
    ///   - pings: the most recent probe of each tunnel, keyed by name.
    ///   - latest: the most recent counter reading of each *watched* tunnel.
    ///     The view's own counters are minutes old by the time anybody reads
    ///     them, and showing those beside a chart drawn from fresher ones is
    ///     the window disagreeing with itself.
    public static func rows(
        _ tunnels: [TunnelStatus],
        pings: [String: Ping] = [:],
        latest: [String: Sample] = [:],
        now: Date,
        locale: Locale = .autoupdatingCurrent
    ) -> [TunnelRow] {
        tunnels.map { tunnel in
            let endpoint = tunnel.endpoint ?? "—"
            guard tunnel.health != .down else {
                return TunnelRow(
                    name: tunnel.name, health: tunnel.health, group: group(tunnel),
                    handshake: "", traffic: "", ping: .none, endpoint: endpoint)
            }

            let reading = latest[tunnel.name]
            let rx = reading?.rx ?? tunnel.rxBytes
            let tx = reading?.tx ?? tunnel.txBytes

            return TunnelRow(
                name: tunnel.name,
                health: tunnel.health,
                group: group(tunnel),
                handshake: tunnel.lastHandshake.map {
                    Formatting.age(now.timeIntervalSince($0))
                } ?? "",
                traffic: "\(Formatting.bytes(rx, locale: locale)) / "
                    + Formatting.bytes(tx, locale: locale),
                ping: cell(pings[tunnel.name]),
                endpoint: endpoint)
        }
    }

    private static func group(_ tunnel: TunnelStatus) -> String {
        tunnel.group.isEmpty ? "no group" : tunnel.group
    }

    private static func cell(_ ping: Ping?) -> TunnelRow.PingCell {
        guard let ping else { return .none }
        guard let rtt = ping.rtt else {
            // A probe with neither a time nor a reason is a publisher that
            // changed its mind mid-round; saying so beats an empty cell that
            // reads as "never asked".
            return .failed(reason: ping.error ?? "no answer")
        }
        // Whole milliseconds, as the terminal shows them. A tenth of a
        // millisecond over a tunnel is noise dressed as precision.
        return .rtt("\(Int((rtt / .milliseconds(1)).rounded()))ms")
    }
}
