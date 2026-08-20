/// Decides when a refusal is news worth opening a panel for.
///
/// It exists because the alternative is a comparison in the AppKit layer, where
/// nothing can be tested. The rule it carries: the same refusal opens a panel
/// once. A menu bar that quietly changes its glyph is one nobody looks at, and
/// this is the one thing this application knows that somebody has to be told —
/// but a panel that comes back on every redraw is a panel somebody learns to
/// dismiss without reading, which costs more than it buys.
public struct WarningPrompt: Sendable {
    private var announced: PublisherWarning?

    public init() {}

    /// Whether this warning should be shown now, and remembers the answer.
    ///
    /// A warning that differs from the last one is news again: a socket that
    /// answers with a third key after somebody trusted a second one is not the
    /// same event, and saying nothing about it would be this application
    /// keeping the interesting part to itself.
    public mutating func opens(for warning: PublisherWarning?) -> Bool {
        defer { announced = warning }
        guard let warning else { return false }
        return warning != announced
    }
}
