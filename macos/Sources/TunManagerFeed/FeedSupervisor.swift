import Foundation

/// Runs the link: turns the machine's actions into tasks, and their outcomes
/// back into events.
///
/// `@MainActor` rather than an `actor`, deliberately. Everything this handles
/// exists to change a menu, and the work is splitting a couple of kilobytes on
/// newlines and decoding it — once every five minutes. What that buys is worth
/// more than the microseconds: no `nonisolated(unsafe)`, no `@unchecked
/// Sendable`, no window where the menu is built from a snapshot that has since
/// been replaced, and no reentrancy to reason about. An `actor` suspends at
/// every `await`, which is exactly how connection managers end up interleaving
/// a stop with the middle of a reconnect.
@MainActor
public final class FeedSupervisor {
    private let transport: any FeedTransport
    private let keys: any PinnedKeys
    private let socketPath: String
    private var machine: LinkMachine
    private var connection: (any FeedConnection)?
    private var reader: Task<Void, Never>?
    private var retry: Task<Void, Never>?
    /// Wakes the machine if the publisher never answers its challenge.
    private var proof: Task<Void, Never>?

    /// Which connection the supervisor is currently interested in.
    ///
    /// Every connection is opened under a number, and nothing it says is
    /// passed on once that number has moved. Cancelling the task that reads it
    /// is not enough on its own: a real close returns before the stream it
    /// feeds has finished, so the tail of a connection this program has walked
    /// away from arrives whenever it arrives -- and delivered to the machine it
    /// reads as "the link just dropped", about a link that is not the one it
    /// describes. That took the menu bar into a loop where every fresh hello
    /// landed in a state that ignores hellos, and only restarting the
    /// application cleared it.
    private var generation = 0

    public weak var observer: (any FeedObserver)?

    /// - Parameters:
    ///   - socketPath: what this client dialled. It goes into the message the
    ///     publisher signs, which is what a relay listening elsewhere cannot
    ///     produce an answer for.
    ///   - keys: where the key each socket's publisher is known by is kept.
    public init(
        transport: any FeedTransport, socketPath: String = "",
        keys: any PinnedKeys = KeychainPinnedKeys(),
        nonces: any NonceSource = SystemNonces()
    ) {
        self.transport = transport
        self.keys = keys
        self.socketPath = socketPath
        self.machine = LinkMachine(
            socketPath: socketPath, nonces: nonces, pinnedKey: keys.pinned(forSocket: socketPath))
    }

    public var state: LinkState { machine.state }
    public var snapshot: Snapshot? { machine.snapshot }
    public var publisherVersion: String? { machine.publisherVersion }
    public var publisherKey: String? { machine.publisherKey }
    /// The most recent probe of each tunnel, keyed by name.
    public var pings: [String: Ping] { machine.pings }

    public func start() { dispatch(.start) }
    public func stop() { dispatch(.stop) }
    public func menuWillOpen() { dispatch(.menuWillOpen) }
    public func systemDidWake() { dispatch(.systemDidWake) }
    public func userAskedToRetry() { dispatch(.userAskedToRetry) }

    /// Forget the key pinned for this socket, and connect again so the next one
    /// offered is pinned in its place.
    ///
    /// The only way out of a refusal, and deliberately the only one: there is
    /// no "trust just this once". Somebody who rotated the key on purpose says
    /// so once and is done; somebody who did not now has an application that
    /// remembers a key they never chose, which is a thing they can see in About
    /// and compare against `sudo tun-manager feed-key`.
    ///
    /// Forgotten in the store before the machine, so that a crash between the
    /// two leaves nothing pinned rather than a pin the store disagrees with.
    public func forgetPinnedKey() {
        keys.forget(socket: socketPath)
        machine.forgetPinnedKey()
        dispatch(.userAskedToRetry)
    }

    /// The detail window is showing this tunnel. Replaces whatever it showed
    /// before, and survives the link dropping: the subscription is sent again
    /// when the next connection greets us.
    public func watch(_ tunnel: String) { dispatch(.watch(tunnel)) }
    public func watchNothing() { dispatch(.watchNothing) }

    /// Asks tun-manager to probe a tunnel's check address, or every one it
    /// knows when the name is nil.
    ///
    /// This is the one thing this application asks for that has an effect
    /// outside the publisher's process. It names a tunnel, never an address:
    /// what gets probed comes from that tunnel's configuration, so nothing sent
    /// from here can reach somewhere tun-manager was not already told about.
    /// Dropped while there is no connection, and floored by the publisher at
    /// one round every two seconds.
    public func askForPing(_ tunnel: String? = nil) { dispatch(.askForPing(tunnel)) }

    private func dispatch(_ event: LinkEvent) {
        for action in machine.handle(event) {
            perform(action)
        }
        observer?.linkDidChange(
            state: machine.state, snapshot: machine.snapshot,
            publisherVersion: machine.publisherVersion)
    }

    private func perform(_ action: LinkAction) {
        switch action {
        case .connect:
            openConnection()
        case .closeConnection:
            // The number moves first: whatever the connection being dropped
            // says on its way out is about a link this program has already
            // stopped believing in.
            generation &+= 1
            connection?.close()
            connection = nil
            reader?.cancel()
            reader = nil
        case .scheduleRetry(let delay):
            scheduleRetry(after: delay)
        case .cancelRetry:
            retry?.cancel()
            retry = nil
        case .send(let command):
            connection?.send(command.line)
        case .publish(let snapshot, let diff):
            // The drawing observer is told below, from whatever is current;
            // this one carries the diff, which nothing current can reconstruct
            // once the snapshot has been replaced.
            observer?.linkDidPublish(snapshot: snapshot, diff: diff)

        case .publishSample(let sample):
            observer?.linkDidSample(sample)

        case .scheduleAuthTimeout(let delay):
            proof?.cancel()
            proof = Task { [weak self] in
                try? await Task.sleep(for: delay)
                guard !Task.isCancelled else { return }
                self?.dispatch(.authTimedOut)
            }
        case .cancelAuthTimeout:
            proof?.cancel()
            proof = nil

        case .pin(let key):
            // Trust on first use: written where it survives a restart, so that
            // every connection after this one is compared against it.
            keys.pin(key, forSocket: socketPath)
        }
    }

    private func openConnection() {
        // Closed rather than let go of. Left to its deinit, an abandoned
        // connection is closed at some point after the next one is up, and its
        // last gasp arrives against a link it knows nothing about.
        connection?.close()
        connection = nil
        reader?.cancel()

        generation &+= 1
        let mine = generation

        reader = Task { [transport] in
            let open: any FeedConnection
            do {
                open = try await transport.connect()
            } catch let failure as ConnectFailure {
                guard self.serving(mine) else { return }
                self.dispatch(.connectFailed(failure.code))
                return
            } catch let refusal as PublisherNotRoot {
                guard self.serving(mine) else { return }
                self.dispatch(.publisherNotRoot(uid: refusal.uid))
                return
            } catch is CancellationError {
                return
            } catch {
                guard self.serving(mine) else { return }
                self.dispatch(.streamFailed)
                return
            }

            // The connection this task went to fetch may have arrived after
            // somebody asked for another one. Closed here, because nothing else
            // holds it.
            guard self.serving(mine) else {
                open.close()
                return
            }
            self.connection = open

            do {
                // Local to this task, and that is the invariant: a framer kept
                // across connections would carry the half-line a crashed
                // publisher left behind into the first line of the next one.
                var framer = LineFramer()
                for try await chunk in open.chunks {
                    guard self.serving(mine) else { return }
                    for line in framer.push(chunk) {
                        if let message = FeedDecoder.decode(line) {
                            self.dispatch(.message(message))
                        }
                    }
                }
                guard self.serving(mine) else { return }
                self.dispatch(.endOfStream)
            } catch is CancellationError {
                // Someone asked; the machine already knows.
            } catch {
                guard self.serving(mine) else { return }
                // Whatever ended the read - a peer that went away, ECANCELED
                // from this program's own close - it is a stream that stopped,
                // not a connect that failed. Reported as a connect failure it
                // became "cannot reach tun-manager (error 89)" about a socket
                // this program was already reading.
                self.dispatch(.streamFailed)
            }
        }
    }

    /// Whether a connection is still the one this supervisor is reading.
    private func serving(_ number: Int) -> Bool { number == generation }

    private func scheduleRetry(after delay: Duration) {
        retry?.cancel()
        retry = Task {
            // ContinuousClock, not SuspendingClock: it advances across system
            // sleep, so a machine that slept for six hours retries at once on
            // waking rather than serving out a stretched deadline.
            try? await Task.sleep(for: delay, clock: .continuous)
            guard !Task.isCancelled else { return }
            self.dispatch(.retryTimerFired)
        }
    }
}
