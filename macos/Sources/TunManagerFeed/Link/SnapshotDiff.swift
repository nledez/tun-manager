/// What changed between two views.
///
/// Deliberately the same semantics as internal/notify's Diff on the Go side: a
/// tunnel absent from either view is not a health change, because the first
/// view has nothing to compare against and a removed config is not an outage.
/// Version 1 only uses `appeared`; the rest is what the notifications will be
/// built from.
public struct SnapshotDiff: Sendable, Equatable {
    public struct HealthChange: Sendable, Equatable {
        public let tunnel: String
        public let from: Health
        public let to: Health
    }

    public let appeared: [String]
    public let disappeared: [String]
    public let healthChanges: [HealthChange]

    public var isEmpty: Bool {
        appeared.isEmpty && disappeared.isEmpty && healthChanges.isEmpty
    }

    /// Everything is reported in tunnel-name order, so that anything built from
    /// a diff — a notification, a log line — comes out in a stable order rather
    /// than in whatever order a dictionary happened to iterate.
    public static func between(_ previous: Snapshot?, and current: Snapshot) -> SnapshotDiff {
        guard let previous else {
            // The first view: everything is new, and nothing has changed
            // health. Treating it as a burst of changes is how a program
            // notifies about every tunnel at startup.
            return SnapshotDiff(
                appeared: current.tunnels.map(\.name).sorted(),
                disappeared: [], healthChanges: [])
        }

        let before = Dictionary(uniqueKeysWithValues: previous.tunnels.map { ($0.name, $0.health) })
        let after = Dictionary(uniqueKeysWithValues: current.tunnels.map { ($0.name, $0.health) })

        var changes: [HealthChange] = []
        for (name, health) in after {
            guard let was = before[name], was != health else { continue }
            changes.append(HealthChange(tunnel: name, from: was, to: health))
        }

        return SnapshotDiff(
            appeared: after.keys.filter { before[$0] == nil }.sorted(),
            disappeared: before.keys.filter { after[$0] == nil }.sorted(),
            healthChanges: changes.sorted { $0.tunnel < $1.tunnel })
    }
}
