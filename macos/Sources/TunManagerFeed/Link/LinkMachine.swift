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

    /// Attempts since the last accepted hello. **Reset by an accepted hello and
    /// by nothing else** — in particular not by a successful connect(2), because
    /// tun-manager accepts and immediately closes connections while it is
    /// shutting down, and a counter reset there would spin at full speed for
    /// the whole shutdown.
    private var attempt = 0

    public init() {}

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

        case (.connecting, .message(.hello(let schema, let version))):
            publisherVersion = version
            guard schema == Self.schema else {
                state = .blocked(theirSchema: schema)
                return [.closeConnection]
            }
            attempt = 0
            state = .live(sawState: false)
            return []

        // Accepted, then closed without a hello: tun-manager is going away.
        case (.connecting, .endOfStream), (.connecting, .streamFailed):
            return retry(because: .rejected)

        case (.live, .message(.state(let view))):
            let diff = SnapshotDiff.between(snapshot, and: view)
            snapshot = view
            state = .live(sawState: true)
            return [.publish(view, diff)]

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

        // Version 1 never watches anything, so this cannot arrive. Ignoring it
        // costs nothing; crashing on it would be a poor way to meet a feature.
        case (.live, .message(.sample)):
            return []

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
