import Foundation
import Testing

@testable import TunManagerFeed

// The key, nonce and signature below come from internal/feed on the Go side, so
// what is exercised here is the agreement rather than this file's idea of it.
private let key = Proven.key
private let nonce = Proven.nonce
private let signature = Proven.signature
private let socket = Proven.socket
private let otherKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

/// A machine that draws the nonce the signature above answers.
private func proving(pinned: String? = nil) -> LinkMachine {
    var machine = Proven.machine(pinned: pinned)
    _ = machine.handle(.start)
    return machine
}

private func hello(_ machine: inout LinkMachine, key: String? = key, schema: Int = 2)
    -> [LinkAction]
{
    machine.handle(.message(.hello(schema: schema, version: Proven.version, publicKey: key)))
}

@Test func aHelloIsAnsweredWithAChallengeRatherThanTrusted() {
    // The publisher has said which key it holds. Believing that without asking
    // is believing whatever is at the end of the socket.
    var machine = proving()

    let actions = hello(&machine)

    #expect(actions.contains(.send(.challenge(nonce))))
    #expect(machine.isLive == false)
}

@Test func aGoodSignatureMakesTheLinkLive() {
    var machine = proving()
    _ = hello(&machine)

    _ = machine.handle(.message(.auth(nonce: nonce.base64EncodedString(), signature: signature)))

    #expect(machine.isLive)
}

@Test func thePublisherIsPinnedTheFirstTimeItProvesItself() {
    // Trust on first use, the way ssh treats a host key: nothing to configure,
    // and every connection after this one is compared against it.
    var machine = proving()
    _ = hello(&machine)

    let actions = machine.handle(
        .message(.auth(nonce: nonce.base64EncodedString(), signature: signature)))

    #expect(actions.contains(.pin(key)))
}

@Test func aPublisherAlreadyPinnedIsNotPinnedAgain() {
    var machine = proving(pinned: key)
    _ = hello(&machine)

    let actions = machine.handle(
        .message(.auth(nonce: nonce.base64EncodedString(), signature: signature)))

    #expect(actions.contains { if case .pin = $0 { return true } else { return false } } == false)
    #expect(machine.isLive)
}

@Test func aSignatureFromAnotherKeyLeavesTheLinkUnproven() {
    // Somebody else's publisher on that socket, or the same one with a new key.
    // Either way this application has no business showing what it says.
    var machine = proving(pinned: otherKey)
    _ = hello(&machine)

    let actions = machine.handle(
        .message(.auth(nonce: nonce.base64EncodedString(), signature: signature)))

    #expect(machine.isLive == false)
    #expect(actions.contains(.closeConnection))
    guard case .unproven(let pinned, let offered) = machine.state else {
        Issue.record("state = \(machine.state), want it unproven")
        return
    }
    #expect(pinned == otherKey)
    #expect(offered == key)
}

@Test func anAnswerToAnotherQuestionIsNotAnAnswer() {
    // The nonce is this connection's. An answer carrying another one is an
    // answer somebody kept from an earlier connection.
    var machine = proving()
    _ = hello(&machine)

    _ = machine.handle(
        .message(
            .auth(nonce: Data(repeating: 1, count: 32).base64EncodedString(), signature: signature)))

    #expect(machine.isLive == false)
}

@Test func aPublisherThatAnnouncesNoKeyIsNotTalkedTo() {
    // It cannot be told from any other program listening on that socket, which
    // is exactly what pinning exists to answer.
    var machine = proving()

    let actions = hello(&machine, key: nil)

    #expect(machine.isLive == false)
    #expect(actions.contains(.closeConnection))
}

@Test func aPublisherThatNeverAnswersIsGivenUpOn() {
    // Something that accepts the connection and says nothing more would
    // otherwise hold the link open forever showing nothing.
    var machine = proving()
    _ = hello(&machine)

    let actions = machine.handle(.authTimedOut)

    #expect(machine.isLive == false)
    #expect(actions.contains(.closeConnection))
}

@Test func theWaitForAnAnswerIsBounded() {
    var machine = proving()

    let actions = hello(&machine)

    #expect(
        actions.contains { if case .scheduleAuthTimeout = $0 { return true } else { return false } })
}

@Test func nothingIsShownBeforeThePublisherHasProvedItself() {
    // A state line arriving between the hello and the answer is a state line
    // from something that has not yet said who it is.
    var machine = proving()
    _ = hello(&machine)

    let actions = machine.handle(.message(.state(aSnapshot())))

    #expect(actions.isEmpty)
    #expect(machine.snapshot == nil)
}

@Test func whatArrivedWhileWaitingIsShownOnceItIsProved() {
    // Held rather than dropped: the publisher sends its view immediately after
    // the hello, and throwing it away would leave the menu empty until the next
    // refresh, which is five minutes later.
    var machine = proving()
    _ = hello(&machine)
    _ = machine.handle(.message(.state(aSnapshot())))

    let actions = machine.handle(
        .message(.auth(nonce: nonce.base64EncodedString(), signature: signature)))

    #expect(actions.contains { if case .publish = $0 { return true } else { return false } })
    #expect(machine.snapshot != nil)
}

@Test func aPublisherSpeakingAnotherSchemaIsRefusedBeforeAnyOfThis() {
    var machine = proving()

    _ = hello(&machine, schema: 1)

    guard case .blocked(let theirs) = machine.state else {
        Issue.record("state = \(machine.state), want it blocked")
        return
    }
    #expect(theirs == 1)
}

private func aSnapshot() -> Snapshot {
    Snapshot(
        context: FeedContext(name: "office"), taken: Date(timeIntervalSince1970: 1),
        tunnels: [TunnelStatus(name: "alpha", group: "needed", health: .up)])
}
