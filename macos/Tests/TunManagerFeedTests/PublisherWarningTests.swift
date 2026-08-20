import Testing

@testable import TunManagerFeed

private let pinned = "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="
private let offered = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

@Test func aChangedKeyIsExplainedAsAChoiceRatherThanAnError() {
    // Two things look identical from here: a key somebody rotated, and somebody
    // else on that socket. Only the person reading can tell them apart, and the
    // window's job is to give them what that takes.
    let warning = PublisherWarning(pinned: pinned, offered: offered, socketPath: "/var/run/x.sock")

    #expect(warning.title.contains("not the tun-manager"))
    let text = warning.details.joined(separator: "\n")
    #expect(text.contains(Fingerprint.of(base64: pinned)!))
    #expect(text.contains(Fingerprint.of(base64: offered)!))
    #expect(text.contains("sudo tun-manager feed-key"))
    #expect(text.contains("/var/run/x.sock"))
}

@Test func theWayOutIsNamedAndSaysWhatItDoes() {
    // "Trust" on its own is a word people click. This one says what it forgets
    // and what happens next.
    let warning = PublisherWarning(pinned: pinned, offered: offered, socketPath: "/var/run/x.sock")

    #expect(warning.accept == "Trust the New Key")
    #expect(warning.dismiss == "Keep Refusing")
    #expect(warning.details.joined(separator: "\n").contains("forget"))
}

@Test func neverTheKeysThemselves() {
    // The window is screenshotted into issues. Fingerprints are derived from
    // the public halves and say everything the comparison needs.
    let warning = PublisherWarning(pinned: pinned, offered: offered, socketPath: "/var/run/x.sock")
    let text = warning.title + warning.details.joined(separator: "\n")

    #expect(text.contains(pinned) == false)
    #expect(text.contains(offered) == false)
}

@Test func aPublisherThatAnnouncedNoKeyGetsItsOwnExplanation() {
    // Nothing to compare: whatever is there cannot be told from any other
    // program listening on that socket.
    let warning = PublisherWarning(pinned: pinned, offered: nil, socketPath: "/var/run/x.sock")

    let text = warning.details.joined(separator: "\n")
    #expect(text.contains("announced no key"))
    #expect(text.contains(Fingerprint.of(base64: pinned)!))
}

@Test func aFirstConnectionThatCannotProveItselfIsExplainedToo() {
    // Nothing pinned, and the publisher still failed: it answered wrongly, or
    // did not answer. Nobody's honest mistake.
    let warning = PublisherWarning(pinned: nil, offered: offered, socketPath: "/var/run/x.sock")

    let text = warning.details.joined(separator: "\n")
    #expect(text.contains("could not prove"))
}

@Test func nothingPinnedAndNothingAnnouncedIsStillAboutTheMissingKey() {
    // Nothing to have failed to prove: with no key on offer there is nothing to
    // check, and saying it failed a check it was never given would be wrong
    // about what happened.
    let warning = PublisherWarning(pinned: nil, offered: nil, socketPath: "/var/run/x.sock")

    #expect(warning.details.joined(separator: "\n").contains("announced no key"))
}

@Test func thereIsNothingToWarnAboutWhenTheLinkIsFine() {
    // The window is offered from the menu only while there is something to
    // read, or it becomes a thing people click and learn nothing from.
    #expect(PublisherWarning.of(state: .live(sawState: true), socketPath: "/x") == nil)
    #expect(PublisherWarning.of(state: .connecting, socketPath: "/x") == nil)
    #expect(
        PublisherWarning.of(state: .unproven(pinned: pinned, offered: offered), socketPath: "/x")
            != nil)
}

@Test func eachFingerprintGetsALineToItself() {
    // A panel lays this out in a proportional font. A fingerprint sharing a
    // line with its label is one that wraps in the middle, and the middle is
    // where two keys stop looking alike.
    let warning = PublisherWarning(pinned: pinned, offered: offered, socketPath: "/x")

    #expect(warning.details.contains(Fingerprint.of(base64: pinned)!))
    #expect(warning.details.contains(Fingerprint.of(base64: offered)!))
}
