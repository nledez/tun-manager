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
    private var machine = LinkMachine()
    private var connection: (any FeedConnection)?
    private var reader: Task<Void, Never>?
    private var retry: Task<Void, Never>?

    public weak var observer: (any FeedObserver)?

    public init(transport: any FeedTransport) {
        self.transport = transport
    }

    public var state: LinkState { machine.state }
    public var snapshot: Snapshot? { machine.snapshot }
    public var publisherVersion: String? { machine.publisherVersion }
    /// The most recent probe of each tunnel, keyed by name.
    public var pings: [String: Ping] { machine.pings }

    public func start() { dispatch(.start) }
    public func stop() { dispatch(.stop) }
    public func menuWillOpen() { dispatch(.menuWillOpen) }
    public func systemDidWake() { dispatch(.systemDidWake) }
    public func userAskedToRetry() { dispatch(.userAskedToRetry) }

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
        }
    }

    private func openConnection() {
        reader?.cancel()
        reader = Task { [transport] in
            do {
                let open = try await transport.connect()
                self.connection = open

                // Local to this task, and that is the invariant: a framer kept
                // across connections would carry the half-line a crashed
                // publisher left behind into the first line of the next one.
                var framer = LineFramer()
                for try await chunk in open.chunks {
                    for line in framer.push(chunk) {
                        if let message = FeedDecoder.decode(line) {
                            self.dispatch(.message(message))
                        }
                    }
                }
                self.dispatch(.endOfStream)
            } catch let failure as ConnectFailure {
                self.dispatch(.connectFailed(failure.code))
            } catch is CancellationError {
                // Someone asked; the machine already knows.
            } catch {
                self.dispatch(.streamFailed)
            }
        }
    }

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
