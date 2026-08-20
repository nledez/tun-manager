import Foundation

/// Turns what is known into what the menu shows.
public enum MenuModelBuilder {
    public static func build(
        state: LinkState,
        snapshot: Snapshot?,
        now: Date,
        locale: Locale = .autoupdatingCurrent
    ) -> MenuModel {
        MenuModel(
            headline: headline(state: state, snapshot: snapshot),
            sections: sections(snapshot: snapshot, now: now, locale: locale),
            footnote: footnote(state: state, snapshot: snapshot, now: now),
            canRefresh: state.isLive,
            showsOverview: snapshot != nil)
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

    /// What to say about a publisher that did not prove itself.
    ///
    /// Three different things, and they are not interchangeable. A publisher
    /// that announced no key is one this application cannot tell from anything
    /// else on that socket. One whose key is not the pinned one is either a new
    /// key or somebody else, and only the person reading can say which — so
    /// both fingerprints are named, because that is the comparison they have to
    /// make. And nothing pinned with a signature that does not hold is
    /// something that answered wrongly, which is nobody's honest mistake.
    private static func unproven(pinned: String?, offered: String?) -> String {
        guard let pinned else {
            guard offered != nil else {
                return "Whatever is on that socket announced no key — it cannot be tun-manager"
            }
            return "Whatever is on that socket could not prove it holds the key it announced"
        }
        guard let offered, let theirs = Fingerprint.of(base64: offered) else {
            return "The publisher on that socket did not prove it holds the key pinned here "
                + "(\(Fingerprint.of(base64: pinned) ?? "unreadable"))"
        }
        return "That socket now answers with a different key: pinned "
            + "\(Fingerprint.of(base64: pinned) ?? "unreadable"), offered \(theirs)"
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
