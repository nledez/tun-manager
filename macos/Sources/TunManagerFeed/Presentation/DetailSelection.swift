/// What the detail window's sidebar is pointing at.
///
/// An enum rather than an optional name, because "the overview" is a thing to
/// select and not the absence of a selection — and a sentinel string would
/// collide with a tunnel unlucky enough to be called that.
///
/// It lives here rather than beside the window because what each selection asks
/// tun-manager for is a decision, and decisions belong where a test can reach
/// them.
public enum DetailSelection: Hashable, Sendable {
    case overview
    case tunnel(String)

    /// What a double-click in the overview table opens, or nil for a click that
    /// names no one tunnel.
    ///
    /// The table hands over a set rather than a row: empty when the click
    /// landed between rows, and larger when several were selected first. One
    /// name is the only case that says which detail to show — picking whichever
    /// came first out of an unordered set would open a tunnel nobody chose, and
    /// blanking the table on an empty click would answer a question nobody
    /// asked.
    public static func opening(_ names: Set<String>) -> DetailSelection? {
        guard names.count == 1, let name = names.first else { return nil }
        return .tunnel(name)
    }

    /// The tunnel to subscribe to, or nil when there is none.
    ///
    /// The overview draws from the state that arrives anyway, so it needs no
    /// reading a second. Subscribing for it would cost tun-manager a
    /// control-socket read per second per tunnel, to fill a column that changes
    /// every five minutes.
    public var watches: String? {
        guard case .tunnel(let name) = self else { return nil }
        return name
    }

    /// The tunnel to probe. Nil means every one — the same convention
    /// `ClientCommand.ping` uses.
    ///
    /// The overview asks for all of them because it has a latency column for
    /// each. Asking per tunnel as they are selected leaves that column half
    /// filled: the rows nobody happened to click stay blank, which reads as
    /// "no answer" rather than as "never asked", and those are not the same
    /// news.
    public var probes: String? {
        guard case .tunnel(let name) = self else { return nil }
        return name
    }
}
