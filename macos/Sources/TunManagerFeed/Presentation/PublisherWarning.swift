/// What to say about a publisher that did not prove which one it is.
///
/// A value rather than a panel, like About: the wording is decided and tested
/// here, and the interface only shows it. It exists at all because this will
/// not fit on one line of a menu — two fingerprints and what they mean is a
/// paragraph, and a paragraph squeezed into a menu item is a paragraph nobody
/// reads.
public struct PublisherWarning: Sendable, Equatable {
    /// The key this application remembered for that socket, or nil if it never
    /// got as far as remembering one.
    public let pinned: String?
    /// The key whatever is there announced this time, when it announced one.
    public let offered: String?
    public let socketPath: String

    public init(pinned: String?, offered: String?, socketPath: String) {
        self.pinned = pinned
        self.offered = offered
        self.socketPath = socketPath
    }

    /// The warning for a state, or nil when there is nothing to warn about.
    ///
    /// Offered from the menu only while there is something to read: an item
    /// that is always there and usually says "everything is fine" is an item
    /// people stop opening.
    public static func of(state: LinkState, socketPath: String) -> PublisherWarning? {
        guard case .unproven(let pinned, let offered) = state else { return nil }
        return PublisherWarning(pinned: pinned, offered: offered, socketPath: socketPath)
    }

    public var title: String { "This is not the tun-manager you pinned" }

    /// The button that forgets, worded so that it says what it forgets rather
    /// than asking for a feeling about it.
    public var accept: String { "Trust the New Key" }
    public var dismiss: String { "Keep Refusing" }

    /// The paragraph, as separate lines so the panel can lay them out.
    ///
    /// It says what happened, gives the comparison to make, and says what each
    /// button does. Two things look identical from here — a key somebody
    /// rotated, and somebody else on that socket — and only the person reading
    /// can tell them apart.
    public var details: [String] {
        var lines = [summary]
        lines.append("")
        // Each fingerprint on a line of its own, under its label. A panel draws
        // this in a proportional font at whatever width it feels like, and a
        // forty-seven character run of hex pairs put after a label is one that
        // wraps in the middle — which is exactly the thing being compared.
        if let pinned, let fingerprint = Fingerprint.of(base64: pinned) {
            lines.append("Pinned here:")
            lines.append(fingerprint)
        }
        if let offered, let fingerprint = Fingerprint.of(base64: offered) {
            lines.append("Offered now:")
            lines.append(fingerprint)
        }
        lines.append("")
        lines.append(
            "Run `sudo tun-manager feed-key` on this machine. If it prints the key offered "
                + "now, you rotated it and this is your own publisher.")
        lines.append("")
        lines.append(
            "Trusting it makes this application forget the key it had pinned for \(socketPath) "
                + "and remember the next one it is shown. Until then it will not repeat anything "
                + "that publisher says.")
        return lines
    }

    /// One sentence for what happened, because the three cases are not the same
    /// news and the remedy differs. Public because the window shows it where
    /// the table would have been, and the two must not drift apart.
    public var summary: String {
        // Announced nothing comes first: with no key on offer there is nothing
        // to have failed to prove, whether or not something is pinned here.
        guard offered != nil else {
            return "Whatever is listening on \(socketPath) announced no key at all, so it cannot "
                + "be told from any other program that opened that socket."
        }
        guard pinned != nil else {
            return "Whatever is listening on \(socketPath) could not prove it holds the key it "
                + "announced. That is not a mistake an honest publisher makes."
        }
        return "Something is listening on \(socketPath) with a key this application has not "
            + "seen before. Either tun-manager's key was rotated, or this is not tun-manager."
    }
}
