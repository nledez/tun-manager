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
}
