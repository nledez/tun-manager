/// Which build this is.
///
/// Two copies of this application can be running at once — the installed one
/// and one being worked on — and left alike they would fight over everything
/// macOS keys on the bundle identifier: the notification permission, the
/// remembered position in the menu bar, and the defaults domain, where a
/// FeedSocket pointed at a stand-in publisher would be read by both.
public enum Flavour: Sendable, Equatable {
    case release
    case development

    public init(bundleIdentifier: String?) {
        // No bundle at all means `swift run`, which is development by
        // definition — and treating it as a release would have it fight the
        // installed application for the position it remembers.
        guard let bundleIdentifier else {
            self = .development
            return
        }
        self = bundleIdentifier.hasSuffix(".dev") ? .development : .release
    }

    /// macOS remembers a status item's position under this name, and lets the
    /// user drag it or hide it. Two builds sharing one name share the answer.
    public var statusItemAutosaveName: String {
        switch self {
        case .release: "net.ledez.tun-manager.menubar.status"
        case .development: "net.ledez.tun-manager.menubar.dev.status"
        }
    }

    /// Whether the menu bar glyph is coloured.
    ///
    /// A release keeps a template image: the menu bar tints it itself and it
    /// stays legible over any wallpaper, which is why health is carried by the
    /// shape and never by colour. A development build breaks that rule on
    /// purpose — with two identical shields in the menu bar, colour is the only
    /// thing that says which is which, and the cost is paid by nobody but the
    /// person who built it.
    public var isTinted: Bool { self == .development }

    /// Shown in the About panel, so a screenshot says which build took it.
    public var label: String? {
        switch self {
        case .release: nil
        case .development: "development build"
        }
    }
}
