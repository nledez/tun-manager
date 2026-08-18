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
    }

    var attempts: Int { state.withLock(\.attempts) }
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

    func connect() async throws -> any FeedConnection {
        let next = state.withLock { state -> Attempt in
            state.attempts += 1
            return state.script.isEmpty ? .refuse(ENOENT) : state.script.removeFirst()
        }

        switch next {
        case .refuse(let code):
            throw ConnectFailure(code: code)
        case .deliver(let lines):
            return FakeConnection(lines: lines, failing: false, transport: self)
        case .deliverThenFail(let lines):
            return FakeConnection(lines: lines, failing: true, transport: self)
        case .deliverAndStayOpen(let lines):
            let connection = FakeConnection(
                lines: lines, failing: false, transport: self, staysOpen: true)
            state.withLock { $0.live = connection }
            return connection
        }
    }
}

private struct FailedRead: Error {}

final class FakeConnection: FeedConnection, @unchecked Sendable {
    let chunks: AsyncThrowingStream<Data, any Error>
    private let transport: FakeTransport
    private let handle: Mutex<AsyncThrowingStream<Data, any Error>.Continuation?>

    init(lines: [String], failing: Bool, transport: FakeTransport, staysOpen: Bool = false) {
        self.transport = transport
        let box = Mutex<AsyncThrowingStream<Data, any Error>.Continuation?>(nil)
        self.chunks = AsyncThrowingStream { continuation in
            box.withLock { $0 = continuation }
            for line in lines {
                continuation.yield(Data(line.utf8))
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
    func close() {}
}
