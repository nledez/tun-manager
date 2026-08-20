import Darwin
import Foundation

/// The connection's state machine.
///
/// Pure: no clock, no I/O, no tasks. Events go in, a new state and a list of
/// actions come out, in the shape internal/tui already uses. Everything hard
/// about this program lives here, which is why everything here is testable.
public struct LinkMachine {
    /// The schema this build understands. A publisher announcing anything else
    /// is refused rather than guessed at.
    ///
    /// Two, because a publisher speaking it can be asked to prove which one it
    /// is. Accepting one as well would mean accepting a publisher that cannot
    /// be asked, which is what anything standing in for tun-manager would
    /// rather be taken for.
    public static let schema = 2

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

    /// The socket this client dialled. It goes into the message the publisher
    /// signs, which is what stops something listening elsewhere from forwarding
    /// a challenge to the real tun-manager and passing the answer off as its
    /// own.
    private let socketPath: String

    /// Where the nonce for each challenge comes from. Injected so a test can
    /// hand over a known one; in the application it is the system's randomness.
    private let nonces: any NonceSource

    /// The key this socket's publisher is known by, or nil the first time. Set
    /// by whoever owns the store, and updated when a `pin` action is carried
    /// out.
    public internal(set) var pinnedKey: String?

    /// The nonce sent on this connection, waiting to be answered.
    private var challenge: Data?

    /// What arrived between the hello and the answer. Held rather than shown:
    /// the publisher sends its view immediately, and dropping it would leave
    /// the menu blank until the next refresh five minutes later.
    private var pending: Snapshot?

    public init(
        socketPath: String = "", nonces: any NonceSource = SystemNonces(), pinnedKey: String? = nil
    ) {
        self.socketPath = socketPath
        self.nonces = nonces
        self.pinnedKey = pinnedKey
    }

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

    /// How long the publisher has to answer. Long enough for a signature over
    /// a hundred bytes on the same machine, short enough that nobody watches a
    /// menu bar wondering.
    static let proofDeadline = Duration.seconds(2)

    /// Decides what an answer to the challenge is worth.
    ///
    /// Verified against the pinned key when there is one, and never against the
    /// key the hello carried: a publisher that has changed its key is exactly
    /// what an impostor looks like, and asking the impostor which key to check
    /// its own signature with would be asking it to mark its own work. The
    /// first connection has nothing pinned, so it takes what it is given and
    /// remembers it — the ssh bargain.
    private mutating func settle(answered: String, signature: String) -> [LinkAction] {
        guard let challenge, let offered = publisherKey,
            Data(base64Encoded: answered) == challenge,
            PublisherProof.holds(
                key: pinnedKey ?? offered, signature: signature, nonce: challenge,
                schema: Self.schema, version: publisherVersion ?? "", path: socketPath)
        else {
            state = .unproven(pinned: pinnedKey, offered: publisherKey)
            return [.closeConnection, .cancelAuthTimeout]
        }

        var actions: [LinkAction] = [.cancelAuthTimeout]
        if pinnedKey == nil {
            pinnedKey = offered
            actions.append(.pin(offered))
        }
        self.challenge = nil
        state = .live(sawState: false)

        // Sorted so the order is the same every time rather than a set's whim.
        // A window opened before tun-manager was running, or left open across a
        // restart, is waiting for exactly this.
        actions += watching.sorted().map { .send(.watch($0)) }
        if let view = pending {
            pending = nil
            actions += handle(.message(.state(view)))
        }
        return actions
    }

    /// Drop what this socket's publisher was known by, so the next connection
    /// pins afresh. Whoever owns the store has already forgotten it there.
    public mutating func forgetPinnedKey() {
        pinnedKey = nil
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

        case (.connecting, .publisherNotRoot(let uid)):
            return retry(because: .notRoot(uid: uid))

        case (.connecting, .message(.hello(let schema, let version, let key))):
            publisherVersion = version
            publisherKey = key
            guard schema == Self.schema else {
                state = .blocked(theirSchema: schema)
                return [.closeConnection]
            }
            guard key != nil else {
                // A publisher that announces no key cannot be told from any
                // other program listening on that socket, which is the one
                // question pinning exists to answer.
                state = .unproven(pinned: pinnedKey, offered: nil)
                return [.closeConnection]
            }
            attempt = 0
            state = .proving
            let nonce = nonces.nonce()
            challenge = nonce
            return [.send(.challenge(nonce)), .scheduleAuthTimeout(Self.proofDeadline)]

        case (.proving, .message(.auth(let answered, let signature))):
            return settle(answered: answered, signature: signature)

        case (.proving, .authTimedOut):
            // Something that accepts a connection and then says nothing would
            // otherwise hold the link open showing nothing at all.
            state = .unproven(pinned: pinnedKey, offered: publisherKey)
            return [.closeConnection]

        // Anything else it says before it has said who it is. Held, not shown.
        case (.proving, .message(.state(let view))):
            pending = view
            return []

        case (.proving, .message):
            return []

        case (.proving, .endOfStream), (.proving, .streamFailed):
            return retry(because: .rejected)

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

        case (.blocked, .userAskedToRetry), (.unproven, .userAskedToRetry):
            // By hand, from the menu, and never on a timer. Reconnecting in a
            // loop against a publisher that will not prove itself is how
            // somebody stops reading the reason it gives.
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
