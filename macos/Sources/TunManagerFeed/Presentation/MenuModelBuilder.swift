import Foundation

/// Turns what is known into what the menu shows.
public enum MenuModelBuilder {
    /// - Parameters:
    ///   - socketPath: what this client dialled, so a refusal can name the
    ///     socket it is about. Empty in the tests that do not care.
    ///   - demo: whether the publisher was named with `--socket`, in which case
    ///     it is not checked for being root and the menu says so for as long as
    ///     it is connected.
    public static func build(
        state: LinkState,
        snapshot: Snapshot?,
        now: Date,
        socketPath: String = "",
        demo: Bool = false,
        locale: Locale = .autoupdatingCurrent
    ) -> MenuModel {
        let warning = PublisherWarning.of(state: state, socketPath: socketPath)
        // A refused publisher takes the whole view down with it, including one
        // left over from a session that was proved. Whatever is on that socket
        // now would otherwise be sitting under a list of tunnel names it did
        // not send, borrowing them, and the difference between "kept from
        // before" and "sent by this" is not a difference a menu can carry.
        let view = warning == nil ? snapshot : nil

        return MenuModel(
            headline: headline(state: state, snapshot: snapshot),
            sections: sections(snapshot: view, now: now, locale: locale),
            footnote: footnote(state: state, snapshot: view, now: now),
            canRefresh: state.isLive,
            showsOverview: view != nil,
            warning: warning,
            // Permanently, not once: an application showing tunnel health from
            // an unprivileged publisher it never checked is telling somebody
            // something about their machine that it has no way to stand behind,
            // and the moment that line is not on screen is the moment the demo
            // gets mistaken for the real one.
            demoNotice: demo ? "Demo publisher — not root, not checked" : nil)
    }

    private static func headline(state: LinkState, snapshot: Snapshot?) -> String {
        switch state {
        case .idle, .connecting:
            return "Connecting to tun-manager…"
        case .proving:
            return "Checking that this is your tun-manager…"
        case .unproven(let pinned, let offered):
            return unproven(pinned: pinned, offered: offered)
        case .retrying(let because):
            return sentence(for: because)
        case .blocked(let theirSchema):
            return "tun-manager speaks schema \(theirSchema); this app understands "
                + "\(LinkMachine.schema). Update whichever is older."
        case .live:
            guard let snapshot else { return "Connected — waiting for the first refresh" }
            return Formatting.context(snapshot.context)
        }
    }

    /// Which of the three refusals happened, in one line.
    ///
    /// One line, because this is a menu: the fingerprints, the comparison to
    /// make and what each remedy costs live in PublisherWarning, shown in a
    /// panel that has room for them. The three cases stay distinct here because
    /// they are not the same news — a publisher that announced no key cannot be
    /// told from anything else on that socket, a key that is not the pinned one
    /// is either a rotation or somebody else, and a signature that does not hold
    /// with nothing pinned is nobody's honest mistake.
    private static func unproven(pinned: String?, offered: String?) -> String {
        guard pinned != nil else {
            guard offered != nil else {
                return "Whatever is on that socket announced no key"
            }
            return "Whatever is on that socket could not prove who it is"
        }
        guard offered != nil else {
            return "The publisher on that socket announced no key"
        }
        return "This is not the tun-manager you pinned"
    }

    /// Each reason gets its own sentence, because each has its own remedy.
    private static func sentence(for reason: Disconnection) -> String {
        switch reason {
        case .notRunning:
            return "tun-manager is not running"
        case .refused:
            return "A socket is there but nothing is listening — tun-manager was killed"
        case .notPermitted:
            return "The socket belongs to root — start tun-manager with sudo, not as root"
        case .rejected, .goodbye:
            return "tun-manager is shutting down"
        case .lost:
            return "Lost the connection to tun-manager"
        case .notRoot(let uid):
            // Named as what it is rather than as a connection problem: the
            // socket answered, and what is behind it is not a program running
            // as root, so it is not tun-manager whatever else it may be.
            guard let uid else {
                return "Something is on that socket and will not say who it is running as"
            }
            return "Something running as uid \(uid) is on that socket — tun-manager runs as root"
        case .failed(let code):
            return "Cannot reach tun-manager (error \(code))"
        }
    }

    private static func sections(snapshot: Snapshot?, now: Date, locale: Locale)
        -> [MenuModel.Section]
    {
        guard let snapshot, !snapshot.tunnels.isEmpty else { return [] }

        // Grouped, then sorted by name inside each group, matching the table.
        // Ungrouped tunnels come last, under no heading: there is no group to
        // name and inventing one would be a lie.
        let byGroup = Dictionary(grouping: snapshot.tunnels, by: \.group)
        let headers =
            byGroup.keys
            .filter { !$0.isEmpty }
            .sorted { left, right in
                let (l, r) = (rank(of: left), rank(of: right))
                return l == r ? left < right : l < r
            } + (byGroup[""] != nil ? [""] : [])

        return headers.map { group in
            MenuModel.Section(
                header: group.isEmpty ? nil : group,
                rows: (byGroup[group] ?? [])
                    .sorted { $0.name < $1.name }
                    .map { row($0, now: now, locale: locale) })
        }
    }

    /// Keeps the always-on tunnels at the top, the way internal/app orders the
    /// table. Alphabetically `extra` sorts above `needed`, which reads as
    /// though the optional tunnels were the important ones.
    private static func rank(of group: String) -> Int {
        switch group {
        case GroupName.needed: 0
        case GroupName.extra: 1
        default: 2
        }
    }

    /// The row carries a name and a symbol and nothing else. What the tunnel
    /// is doing lives in TunnelDetail, shown by the window: the facts fit in a
    /// menu but the traffic does not, and having both build the same list was
    /// two places to keep in step.
    private static func row(_ tunnel: TunnelStatus, now: Date, locale: Locale) -> MenuModel.Row {
        MenuModel.Row(title: tunnel.name, symbol: symbol(for: tunnel.health))
    }

    private static func symbol(for health: Health) -> String {
        switch health {
        case .up: "checkmark.circle.fill"
        case .stale: "exclamationmark.triangle.fill"
        case .down: "xmark.circle"
        case .unknown: "questionmark.circle"
        }
    }

    private static func footnote(state: LinkState, snapshot: Snapshot?, now: Date) -> String {
        guard let snapshot else { return "Nothing known yet" }
        let age = Formatting.age(now.timeIntervalSince(snapshot.taken))
        // A view kept across a disconnection has to say so, or it reads as
        // current — which is the failure this whole application exists to
        // avoid.
        return state.isLive ? "Updated \(age) ago" : "Last known \(age) ago"
    }
}

extension LinkState {
    /// Whether the feed is currently answering.
    public var isLive: Bool {
        if case .live = self { return true }
        return false
    }
}
