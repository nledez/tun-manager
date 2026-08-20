import Foundation

/// Everything the menu shows, decided.
///
/// Pure data. The renderer that turns this into NSMenuItems makes no decisions
/// at all — no formatting, no sorting, no conditionals. An `if` in the renderer
/// means something belongs here instead, where it can be tested.
public struct MenuModel: Sendable, Equatable {
    /// One tunnel.
    public struct Row: Sendable, Equatable {
        public let title: String
        /// SF Symbol, drawn beside the name.
        public let symbol: String
    }

    /// A group of tunnels. `header` is nil for tunnels in no group, which are
    /// listed without one rather than under an invented heading.
    public struct Section: Sendable, Equatable {
        public let header: String?
        public let rows: [Row]
    }

    /// The first line: the network context when connected, why not when not.
    public let headline: String
    public let sections: [Section]
    /// How old the view is, or what is being waited for.
    public let footnote: String
    /// False while there is nothing to ask.
    public let canRefresh: Bool
    /// Whether to offer the whole table. False before anything has arrived: the
    /// window would open on "No tunnels" and say nothing the headline does not.
    public let showsOverview: Bool
    /// What to explain about a publisher that did not prove which one it is,
    /// or nil when there is nothing to explain.
    ///
    /// Separate from the headline because it does not fit in one: a menu line
    /// carrying two fingerprints and a remedy is a menu line nobody reads. The
    /// headline says which of the three things happened, and this is offered
    /// beside it for the rest.
    public let warning: PublisherWarning?
    /// Set for as long as the publisher is one the user named with `--socket`,
    /// which is not required to be root and is therefore not checked for it.
    public let demoNotice: String?
}
