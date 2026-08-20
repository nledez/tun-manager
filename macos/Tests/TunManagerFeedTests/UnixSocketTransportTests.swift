import Darwin
import Foundation
import Testing

@testable import TunManagerFeed

/// Serialised on purpose.
///
/// These are the only tests that open real descriptors, and swift-testing runs
/// everything in parallel by default. A hundred tests creating and tearing down
/// Dispatch channels at once trips libdispatch's own assertion about a
/// descriptor going away under an active channel — reproducibly, a few runs in
/// ten, with AddressSanitizer reporting nothing because it is a trap rather
/// than a memory error.
///
/// It is a property of the harness and not of the program: in use there is one
/// connection at a time, created and closed by the supervisor on the main
/// actor. Serialising here makes the suite deterministic without pretending the
/// program has a problem it does not.
extension RealSockets {
@Suite struct Transport {

/// A connection built directly on one end of a socketpair, so these tests prove
/// what only the kernel can prove without needing a path, a listener or a
/// permission.
private func pair() throws -> (mine: Int32, theirs: Int32) {
    var fds: [Int32] = [0, 0]
    guard socketpair(AF_UNIX, SOCK_STREAM, 0, &fds) == 0 else {
        throw ConnectFailure(code: errno)
    }
    return (fds[0], fds[1])
}

private func write(_ text: String, to fd: Int32) {
    let bytes = Array(text.utf8)
    _ = bytes.withUnsafeBufferPointer { Darwin.write(fd, $0.baseAddress, $0.count) }
}

@Test func bytesWrittenToOneEndOfASocketPairArriveAsChunksAtTheOther() async throws {
    let (mine, theirs) = try pair()
    let connection = UnixSocketConnection(descriptor: mine)
    defer { connection.close(); close(theirs) }

    write("hello\n", to: theirs)
    close(theirs)

    var seen = Data()
    for try await chunk in connection.chunks { seen.append(chunk) }

    #expect(String(decoding: seen, as: UTF8.self) == "hello\n")
}

@Test func aSingleByteIsDeliveredWithoutWaitingForMore() async throws {
    // This is the test that pins setLimit(lowWater: 1). Dispatch documents the
    // default low-water mark as *unspecified*, so the channel may sit on what
    // it has while it waits for more bytes to arrive. The publisher sends one
    // line every five minutes, so the symptom would be a menu bar minutes
    // behind with no error anywhere.
    //
    // One byte is written and nothing follows it, so this cannot pass by
    // accident on a machine that happened to be fast: either the channel hands
    // over what it has, or nothing ever arrives.
    let (mine, theirs) = try pair()
    let connection = UnixSocketConnection(descriptor: mine)
    defer { connection.close(); close(theirs) }

    write("x", to: theirs)

    let arrived = Task { () -> Bool in
        for try await chunk in connection.chunks where !chunk.isEmpty { return true }
        return false
    }
    let deadline = Task { () -> Bool in
        try await Task.sleep(for: .seconds(2))
        connection.close()
        return false
    }
    defer { deadline.cancel() }

    #expect(try await arrived.value, "the byte was withheld rather than handed over")
}

@Test func aWriteToASocketWhosePeerHasGoneDoesNotKillTheProcess() async throws {
    // Writing to a socket with no reader raises SIGPIPE, and its default
    // disposition is to kill the process. tun-manager comes and goes
    // constantly, and this application writes a refresh whenever the menu
    // opens, so without SO_NOSIGPIPE the menu bar item would simply vanish the
    // first time those two coincided.
    let (mine, theirs) = try pair()
    let connection = UnixSocketConnection(descriptor: mine)
    defer { connection.close() }
    close(theirs)

    connection.send(ClientCommand.refresh.line)
    try await Task.sleep(for: .milliseconds(100))

    // Reaching this line at all is the assertion: a SIGPIPE would have taken
    // the whole suite with it.
    #expect(Bool(true))
}

@Test func closingTheWritingEndEndsTheStreamWithoutAnError() async throws {
    let (mine, theirs) = try pair()
    let connection = UnixSocketConnection(descriptor: mine)

    close(theirs)

    // The absence of a throw is the assertion.
    for try await _ in connection.chunks {}
}

@Test func cancellingTheReadingTaskClosesTheFileDescriptor() async throws {
    let (mine, theirs) = try pair()
    defer { close(theirs) }
    let connection = UnixSocketConnection(descriptor: mine)

    let reader = Task { for try await _ in connection.chunks {} }
    try await Task.sleep(for: .milliseconds(50))
    reader.cancel()
    _ = try? await reader.value
    try await Task.sleep(for: .milliseconds(50))

    #expect(fcntl(mine, F_GETFD) == -1, "the descriptor outlived the task that was reading it")
    #expect(errno == EBADF)
}

@Test func aRefreshWrittenToTheConnectionReachesTheOtherEnd() async throws {
    let (mine, theirs) = try pair()
    let connection = UnixSocketConnection(descriptor: mine)
    defer { connection.close(); close(theirs) }

    connection.send(ClientCommand.refresh.line)
    try await Task.sleep(for: .milliseconds(50))

    var buffer = [UInt8](repeating: 0, count: 64)
    let n = read(theirs, &buffer, buffer.count)
    #expect(n > 0)
    #expect(String(decoding: buffer[0..<max(0, n)], as: UTF8.self) == "{\"type\":\"refresh\"}\n")
}

@Test func connectingToAPathThatIsNotThereFailsWithTheErrnoThatSaysSo() async {
    let transport = UnixSocketTransport(path: "/tmp/tm-nothing-is-here-\(UInt32.random(in: 0...9999)).sock")

    await #expect(throws: ConnectFailure(code: ENOENT)) {
        _ = try await transport.connect()
    }
}

@Test func aSocketPathLongerThanTheKernelAllowsIsRefusedBeforeTheSyscall() async {
    // 104 bytes on this SDK. Refusing early means the error names the problem
    // rather than arriving as a puzzling EINVAL from bind.
    let transport = UnixSocketTransport(path: "/tmp/" + String(repeating: "x", count: 200))

    await #expect(throws: SocketPathTooLong.self) {
        _ = try await transport.connect()
    }
}
}
}
