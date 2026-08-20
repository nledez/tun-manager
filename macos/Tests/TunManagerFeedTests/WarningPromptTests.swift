import Testing

@testable import TunManagerFeed

private func warning(offered: String) -> PublisherWarning {
    PublisherWarning(
        pinned: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg=", offered: offered,
        socketPath: "/var/run/x.sock")
}

private let first = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
private let second = "HwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

@Test func theSameRefusalOpensThePanelOnceAndNotOnEveryRedraw() {
    // Every arriving line redraws the menu. A panel that came back each time is
    // one somebody dismisses without reading.
    var prompt = WarningPrompt()

    let opened = prompt.opens(for: warning(offered: first))
    let again = prompt.opens(for: warning(offered: first))
    let onceMore = prompt.opens(for: warning(offered: first))

    #expect(opened)
    #expect(again == false)
    #expect(onceMore == false)
}

@Test func aDifferentKeyOnThatSocketIsNewsAgain() {
    // A third key after somebody trusted a second one is not the same event,
    // and keeping quiet about it would be keeping the interesting part back.
    var prompt = WarningPrompt()
    _ = prompt.opens(for: warning(offered: first))
    let rotatedAgain = prompt.opens(for: warning(offered: second))

    #expect(rotatedAgain)
}

@Test func aLinkThatCameBackAndFailedAgainIsSaidAgain() {
    // Trusting the new key clears the refusal; if the next connection fails to
    // prove itself too, that is a fresh thing to be told about.
    var prompt = WarningPrompt()
    _ = prompt.opens(for: warning(offered: first))
    let recovered = prompt.opens(for: nil)
    let failedAgain = prompt.opens(for: warning(offered: first))

    #expect(recovered == false)
    #expect(failedAgain)
}

@Test func aWorkingLinkOpensNothing() {
    var prompt = WarningPrompt()
    let opened = prompt.opens(for: nil)

    #expect(opened == false)
}
