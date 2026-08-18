import Darwin
import Foundation
import Testing

@testable import TunManagerFeed

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
    defer { close(theirs) }

    write("hello\n", to: theirs)
    close(theirs)

    var seen = Data()
    for try await chunk in connection.chunks { seen.append(chunk) }

    #expect(String(decoding: seen, as: UTF8.self) == "hello\n")
}

@Test func aLineWrittenOneByteAtATimeIsDeliveredAByteAtATime() async throws {
    // This is the test that pins setLimit(lowWater: 1). Dispatch documents the
    // default low-water mark as *unspecified*, so without that call the channel
    // may sit on a complete state line waiting for more bytes — and since the
    // publisher sends one every five minutes, the symptom would be a menu bar
    // minutes behind, with no error anywhere.
    let (mine, theirs) = try pair()
    let connection = UnixSocketConnection(descriptor: mine)

    let reader = Task { () -> Int in
        var chunks = 0
        for try await _ in connection.chunks { chunks += 1 }
        return chunks
    }

    for byte in "abcdef" {
        write(String(byte), to: theirs)
        try await Task.sleep(for: .milliseconds(20))
    }
    close(theirs)

    let chunks = try await reader.value
    #expect(chunks >= 4, "\(chunks) chunk(s): bytes were withheld rather than delivered as they arrived")
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
    defer { close(theirs) }

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
