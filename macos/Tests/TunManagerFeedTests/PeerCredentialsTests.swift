import Darwin
import Foundation
import Testing

@testable import TunManagerFeed

/// Serialised for the reason UnixSocketTransportTests is: these open real
/// descriptors, and a suite running a hundred tests in parallel trips
/// libdispatch's own assertion about a descriptor going away under an active
/// channel. A property of the harness, not of the program - in use there is one
/// connection at a time, opened and closed by the supervisor on the main actor.
extension RealSockets {
@Suite struct Credentials {

private let root = PeerPolicy(requiresRoot: true)
private let demo = PeerPolicy(requiresRoot: false)

@Test func rootIsTheOnlyPeerThisApplicationTalksTo() throws {
    // tun-manager runs as root. Anything else answering on that socket is
    // something else, whatever it says about itself afterwards.
    try root.check(peer: 0)

    #expect(throws: PublisherNotRoot(uid: 501, found: .peer)) { try root.check(peer: 501) }
}

@Test func aCredentialThatCannotBeReadIsARefusal() {
    // "Who is that" being unavailable is not the same as it being acceptable.
    // Every path that treats the two alike is a path somebody arranges to take.
    #expect(throws: PublisherNotRoot(uid: nil, found: .peer)) { try root.check(peer: nil) }
}

@Test func aSocketSomebodyElseBoundIsRefusedBeforeAWordIsRead() throws {
    // The file is about the name, and a name is what somebody who can write the
    // directory replaces.
    try root.check(socketOwner: 0)

    #expect(throws: PublisherNotRoot(uid: 501, found: .socketFile)) {
        try root.check(socketOwner: 501)
    }
}

@Test func noSocketAtAllIsNotSomebodyElsesSocket() throws {
    // The normal state of this machine: there is no daemon. connect(2) is about
    // to say so with an errno that names it, and turning that into "something
    // is not root" would be a worse sentence and a false one.
    try root.check(socketOwner: nil)
}

@Test func theDemoPublisherIsNotRootAndThatIsThePointOfIt() throws {
    // It can reach nothing this user could not reach anyway, which is what
    // makes it safe to run. Requiring root there would mean the demo could only
    // be run in the one way it must never be run.
    try demo.check(peer: 501)
    try demo.check(peer: nil)
    try demo.check(socketOwner: 501)
}

@Test func onlyTheFlagMakesADemo() {
    // A defaults key is not one: it exists for an installation that moved
    // feed_socket, and what is at the end of it is still root's. The flag is
    // the only form that leaves nothing behind, which is why it is the only one
    // that switches a security rule off.
    let defaults = UserDefaults(suiteName: "peer-credentials-tests")!
    defaults.removePersistentDomain(forName: "peer-credentials-tests")

    let flag = SocketPath.chosen(arguments: ["app", "--socket", "/tmp/d.sock"], defaults: defaults)
    #expect(flag == SocketPath.Choice(path: "/tmp/d.sock", isDemo: true))
    #expect(PeerPolicy.of(flag).requiresRoot == false)

    defaults.set("/var/run/moved.sock", forKey: SocketPath.defaultsKey)
    let moved = SocketPath.chosen(arguments: ["app"], defaults: defaults)
    #expect(moved == SocketPath.Choice(path: "/var/run/moved.sock", isDemo: false))
    #expect(PeerPolicy.of(moved).requiresRoot)

    defaults.removePersistentDomain(forName: "peer-credentials-tests")
    let plain = SocketPath.chosen(arguments: ["app"], defaults: defaults)
    #expect(plain == SocketPath.Choice(path: SocketPath.fallback, isDemo: false))
    #expect(PeerPolicy.of(plain).requiresRoot)
}

@Test func theFileAndThePeerAreTwoQuestions() async throws {
    // A socket root bound, answered by somebody who took it over: the owner of
    // the file says root, and the only thing that catches it is asking the
    // kernel who is on the other end right now.
    let publisher = try LocalPublisher()
    defer { publisher.stop() }

    let transport = UnixSocketTransport(
        path: publisher.path, policy: root, peer: { _ in 501 }, owner: { _ in 0 })

    await #expect(throws: PublisherNotRoot(uid: 501, found: .peer)) {
        _ = try await transport.connect()
    }
}

@Test func aRootPeerOnARootSocketIsLetThrough() async throws {
    let publisher = try LocalPublisher()
    defer { publisher.stop() }

    let transport = UnixSocketTransport(
        path: publisher.path, policy: root, peer: { _ in 0 }, owner: { _ in 0 })

    let connection = try await transport.connect()
    connection.close()
}

@Test func theFileIsJudgedBeforeTheConnectIsEvenMade() async throws {
    // Cheapest moment to find out, and the one where nothing has been read from
    // whoever is there. There is no publisher in this test at all: the refusal
    // has to happen without one.
    let transport = UnixSocketTransport(
        path: "/tmp/nothing-is-here.sock", policy: root, peer: { _ in 0 }, owner: { _ in 501 })

    await #expect(throws: PublisherNotRoot(uid: 501, found: .socketFile)) {
        _ = try await transport.connect()
    }
}
}
}
