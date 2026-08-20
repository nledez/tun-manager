import Foundation
import Synchronization

@testable import TunManagerFeed

/// A scripted transport: no kernel, no filesystem, no timing.
///
/// Each element of the script is one connection's worth of behaviour, taken in
/// turn, so a test can say "the first attempt is refused, the second delivers a
/// hello and then ends".
final class FakeTransport: FeedTransport, Sendable {
    enum Attempt {
        /// connect(2) fails with this errno.
        case refuse(Int32)
        /// These chunks arrive, then the stream ends cleanly.
        case deliver([String])
        /// These chunks arrive, then the stream throws.
        case deliverThenFail([String])
        /// These chunks arrive and the connection stays open, the way a real
        /// one does between refreshes. Without this a link would go live and
        /// drop again in the same breath, which is a fact about the fake and
        /// not about the program.
        case deliverAndStayOpen([String])
        /// connect(2) hangs until the test releases this attempt, and then
        /// fails with this errno. What it models is the only thing that makes
        /// a stale answer possible at all: work still in flight for a
        /// connection somebody has already walked away from.
        case stallsThenRefuses(Int32)
        /// These chunks arrive and then the *read* fails with this errno, the
        /// way DispatchIO reports one - as the same error type a failed
        /// connect throws, which is what made the two indistinguishable.
        case deliverThenFailToRead([String], Int32)
    }

    // A mutex rather than NSLock: NSLock's lock() is unavailable from an async
    // context, and connect() is one.
    private let state = Mutex<State>(State())

    private struct State {
        var script: [Attempt] = []
        var attempts = 0
        var sent: [String] = []
        /// The connection currently open, so a test can deliver a line after
        /// something has been asked for rather than only at the start.
        var live: FakeConnection?
        /// Which stalled attempts the test has let go.
        var released: Set<Int> = []
        /// Every connection handed out, in the order they were opened, so a
        /// test can reach back to one that has been left behind.
        var opened: [FakeConnection] = []
    }

    var attempts: Int { state.withLock(\.attempts) }

    /// Lets a stalled attempt finish. Nothing waits on a clock: the test says
    /// when, which is what makes the order of two events something a test can
    /// state rather than hope for.
    func release(_ index: Int) {
        state.withLock { _ = $0.released.insert(index) }
    }

    private func waitForRelease(_ index: Int) async {
        while !state.withLock({ $0.released.contains(index) }) {
            try? await Task.sleep(for: .milliseconds(1))
        }
    }
    var sent: [String] { state.withLock(\.sent) }

    init(_ script: [Attempt]) {
        state.withLock { $0.script = script }
    }

    func record(_ line: Data) {
        state.withLock { $0.sent.append(String(decoding: line, as: UTF8.self)) }
    }

    /// Delivers a line on the open connection, now.
    func push(_ line: String) {
        state.withLock(\.live)?.push(line)
    }

    /// Ends the stream of a connection opened earlier, whenever the test says.
    ///
    /// The real one closes through DispatchIO, whose read handler fires on
    /// another queue: `close()` returns long before the stream it feeds is
    /// finished, so the tail of a connection that has been abandoned can land
    /// after its replacement is already up. That is the race this models, and a
    /// fake whose close() ended the stream at once would never show it.
    func endStream(_ index: Int) {
        state.withLock(\.opened)[index].endStream()
    }

    func connect() async throws -> any FeedConnection {
        let (next, index) = state.withLock { state -> (Attempt, Int) in
            state.attempts += 1
            let attempt = state.script.isEmpty ? Attempt.refuse(ENOENT) : state.script.removeFirst()
            return (attempt, state.attempts - 1)
        }

        let connection: FakeConnection
        switch next {
        case .refuse(let code):
            throw ConnectFailure(code: code)
        case .deliver(let lines):
            connection = FakeConnection(lines: lines, failing: false, transport: self)
        case .deliverThenFail(let lines):
            connection = FakeConnection(lines: lines, failing: true, transport: self)
        case .deliverAndStayOpen(let lines):
            connection = FakeConnection(
                lines: lines, failing: false, transport: self, staysOpen: true)
            state.withLock { $0.live = connection }
        case .stallsThenRefuses(let code):
            await waitForRelease(index)
            throw ConnectFailure(code: code)
        case .deliverThenFailToRead(let lines, let code):
            connection = FakeConnection(
                lines: lines, failing: false, transport: self, staysOpen: true,
                readFailure: ConnectFailure(code: code))
            state.withLock { $0.live = connection }
        }
        state.withLock { $0.opened.append(connection) }
        return connection
    }
}

private struct FailedRead: Error {}

final class FakeConnection: FeedConnection, @unchecked Sendable {
    let chunks: AsyncThrowingStream<Data, any Error>
    private let transport: FakeTransport
    private let handle: Mutex<AsyncThrowingStream<Data, any Error>.Continuation?>

    init(
        lines: [String], failing: Bool, transport: FakeTransport, staysOpen: Bool = false,
        readFailure: (any Error)? = nil
    ) {
        self.transport = transport
        let box = Mutex<AsyncThrowingStream<Data, any Error>.Continuation?>(nil)
        self.chunks = AsyncThrowingStream { continuation in
            box.withLock { $0 = continuation }
            for line in lines {
                continuation.yield(Data(line.utf8))
            }
            if let readFailure {
                continuation.finish(throwing: readFailure)
                return
            }
            guard !staysOpen else { return }
            failing ? continuation.finish(throwing: FailedRead()) : continuation.finish()
        }
        self.handle = box
    }

    func push(_ line: String) {
        handle.withLock { $0 }?.yield(Data(line.utf8))
    }

    func send(_ line: Data) { transport.record(line) }

    /// Like the real one: it returns before the stream it feeds has finished.
    func close() {}

    /// The tail the real close eventually produces, fired when a test asks.
    func endStream() {
        handle.withLock { $0 }?.finish()
    }
}
