/// How a tunnel is doing, as the publisher sees it.
///
/// The wire carries exactly three values today. `unknown` exists so that a
/// publisher which grows a fourth costs this client one row it cannot draw
/// confidently, rather than the whole view.
public enum Health: Sendable, Equatable {
    case up
    case stale
    case down
    case unknown(String)

    public init(wire: String) {
        switch wire {
        case "up": self = .up
        case "stale": self = .stale
        case "down": self = .down
        default: self = .unknown(wire)
        }
    }

    /// Whether the tunnel is carrying anything. `unknown` is not treated as
    /// working: a value this client cannot interpret is not a promise.
    public var isCarrying: Bool { self == .up }
}
