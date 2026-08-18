import Foundation

/// One probe of a tunnel's check address.
///
/// Keyed by tunnel rather than by address because the publisher already knows
/// which address belongs to which name. It is measured by `tun-manager`, which
/// owns the tunnels: the check addresses are reachable only through them, so a
/// client could not take this reading itself even if it wanted to.
public struct Ping: Sendable, Equatable {
    public let tunnel: String
    /// The round trip, or nil when the probe failed. Nil rather than zero
    /// because zero seconds is a measurement and "no answer" is not.
    public let rtt: Duration?
    /// Why it failed, when it did.
    public let error: String?

    public init(tunnel: String, rtt: Duration? = nil, error: String? = nil) {
        self.tunnel = tunnel
        self.rtt = rtt
        self.error = error
    }
}
