import Darwin

/// The connection's state machine.
///
/// Pure: no clock, no I/O, no tasks. Events go in, a new state and a list of
/// actions come out, in the shape internal/tui already uses. Everything hard
/// about this program lives here, which is why everything here is testable.
public struct LinkMachine {
    /// The schema this build understands. A publisher announcing anything else
    /// is refused rather than guessed at.
    public static let schema = 1

    public private(set) var state: LinkState = .idle
    /// The last view received, kept across a disconnection so the menu shows
    /// what it last knew instead of going blank.
    public private(set) var snapshot: Snapshot?
    public private(set) var publisherVersion: String?
    /// The public half of the key the publisher announced, base64, or nil when
    /// it announced none. Shown as a fingerprint, never as itself.
    public private(set) var publisherKey: String?

    /// Attempts since the last accepted hello. **Reset by an accepted hello and
    /// by nothing else** — in particular not by a successful connect(2), because
    /// tun-manager accepts and immediately closes connections while it is
    /// shutting down, and a counter reset there would spin at full speed for
    /// the whole shutdown.
    private var attempt = 0

    /// Every tunnel the detail window has looked at while it has been open.
    ///
    /// A set rather than one name: switching tunnels keeps the old one's
    /// history growing, so going back to it shows an unbroken graph instead of
    /// a gap. They are released together when the window closes.
    ///
    /// Held here rather than by the window because it has to outlive the
    /// connection: the publisher forgets every watch when a connection ends, so
    /// a window left open across a restart needs them sent again.
    private var watching: Set<String> = []

    /// The most recent probe of each tunnel, kept across a disconnection for
    /// the same reason the snapshot is: what was last known beats a blank.
    public private(set) var pings: [String: Ping] = [:]

    public init() {}

    /// What the window is watching, for whoever needs to route a reading.
    public var watched: Set<String> { watching }

    public var isLive: Bool {
        if case .live = state { return true }
        return false
    }

    public var reason: Disconnection? {
        if case .retrying(let because) = state { return because }
        return nil
    }

    /// What the next retry will wait, exposed so a test can watch the ladder
    /// climb rather than infer it from a scheduled action.
    public var retryDelay: Duration? {
        guard let reason else { return nil }
        return ReconnectPolicy.delay(after: reason, attempt: attempt)
    }

    public mutating func handle(_ event: LinkEvent) -> [LinkAction] {
        switch (state, event) {
        case (_, .stop):
            state = .idle
            return [.cancelRetry, .closeConnection]

        case (.idle, .start):
            state = .connecting
            return [.connect]

        case (.connecting, .connectFailed(let code)):
            return retry(because: Self.reason(for: code))

        case (.connecting, .message(.hello(let schema, let version, let key))):
            publisherVersion = version
            publisherKey = key
            guard schema == Self.schema else {
                state = .blocked(theirSchema: schema)
                return [.closeConnection]
            }
            attempt = 0
            state = .live(sawState: false)
            // A window opened before tun-manager was running, or left open
            // across a restart, is waiting for exactly this. Sorted so the
            // order is the same every time rather than a set's whim.
            return watching.sorted().map { .send(.watch($0)) }

        // Accepted, then closed without a hello: tun-manager is going away.
        case (.connecting, .endOfStream), (.connecting, .streamFailed):
            return retry(because: .rejected)

        case (.live, .message(.state(let view))):
            let diff = SnapshotDiff.between(snapshot, and: view)
            snapshot = view
            state = .live(sawState: true)
            // A tunnel that has left the configuration takes its last measured
            // latency with it, so a name reused later cannot inherit it.
            let names = Set(view.tunnels.map(\.name))
            pings = pings.filter { names.contains($0.key) }
            return [.publish(view, diff)]

        case (.live, .message(.ping(let round))):
            // Merged rather than replaced: a round covering one tunnel must not
            // blank out what is known about the others.
            for ping in round {
                pings[ping.tunnel] = ping
            }
            return []

        case (_, .askForPing(let tunnel)):
            // Nothing to queue while the link is down. A watch is restored on
            // the next hello because it is a standing subscription; a probe is
            // a question about right now, and answering it two minutes later
            // would be answering a different question.
            return isLive ? [.send(.ping(tunnel))] : []

        case (.live, .message(.bye)):
            return [.closeConnection] + retry(because: .goodbye)

        case (.live, .endOfStream), (.live, .streamFailed):
            return retry(because: .lost)

        case (.live, .menuWillOpen):
            // The default refresh interval is five minutes. A status app that
            // lies quietly is worse than one that admits it does not know, and
            // the publisher caps these at one every two seconds precisely so
            // asking at the moment somebody looks is safe.
            return [.send(.refresh)]

        case (.live, .message(.sample(let sample))):
            // An unwatch and a reading can cross on the wire. Charting one for
            // a tunnel the window has already left would draw it into the
            // wrong graph.
            guard watching.contains(sample.tunnel) else { return [] }
            return [.publishSample(sample)]

        case (_, .watch(let tunnel)):
            // The previous tunnel keeps its subscription: its history goes on
            // filling, so coming back to it shows a continuous graph rather
            // than a gap where nobody was looking.
            guard watching.insert(tunnel).inserted else { return [] }
            // Nothing to send while there is no connection; the hello above
            // sends them all once there is one.
            return isLive ? [.send(.watch(tunnel))] : []

        case (_, .watchNothing):
            guard !watching.isEmpty else { return [] }
            let released = watching.sorted()
            watching = []
            return isLive ? released.map { .send(.unwatch($0)) } : []

        case (.retrying, .retryTimerFired),
            (.retrying, .userAskedToRetry),
            (.retrying, .systemDidWake),
            (.retrying, .menuWillOpen):
            attempt += 1
            state = .connecting
            return [.cancelRetry, .connect]

        case (.blocked, .userAskedToRetry):
            attempt = 0
            state = .connecting
            return [.connect]

        default:
            return []
        }
    }

    private mutating func retry(because reason: Disconnection) -> [LinkAction] {
        state = .retrying(because: reason)
        return [.scheduleRetry(ReconnectPolicy.delay(after: reason, attempt: attempt))]
    }

    private static func reason(for code: Int32) -> Disconnection {
        switch code {
        case ENOENT: .notRunning
        case ECONNREFUSED: .refused
        case EACCES, EPERM: .notPermitted
        default: .failed(code)
        }
    }
}
