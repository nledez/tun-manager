import Testing

@testable import TunManagerFeed

@Test func aboutNamesBothVersionsAndTheSocket() {
    // Two programs are involved and they version separately, so "which one am I
    // looking at" needs both answers, labelled.
    let about = About(
        appVersion: "0.3.0", build: "v0.3.0-14-g0b52d9e",
        publisherVersion: "v0.2.0", socketPath: "/var/run/tun-manager.sock")

    #expect(about.title == "Tun Manager 0.3.0")
    #expect(about.details.contains { $0.contains("v0.3.0-14-g0b52d9e") })
    #expect(about.details.contains { $0.contains("v0.2.0") })
    #expect(about.details.contains { $0.contains("/var/run/tun-manager.sock") })
}

@Test func aboutSaysSoWhenItHasNotBeenToldThePublishersVersion() {
    // Before the first hello there is nothing to report, and an empty line
    // would read as a version rather than as an absence.
    let about = About(
        appVersion: "0.3.0", build: "dev", publisherVersion: nil,
        socketPath: "/var/run/tun-manager.sock")

    #expect(about.details.contains { $0.contains("not connected") })
}

@Test func aboutDoesNotRepeatTheVersionWhenTheBuildAddsNothing() {
    // A release build's describe is just the tag, and printing "0.3.0 (v0.3.0)"
    // says the same thing twice.
    let about = About(
        appVersion: "0.3.0", build: "v0.3.0", publisherVersion: nil, socketPath: "/tmp/f.sock")

    #expect(!about.details.contains { $0.hasPrefix("Build") })
}

@Test func aDevelopmentBuildSaysSoInItsTitle() {
    // So a screenshot of the panel carries the answer to "which one was that".
    let about = About(
        appVersion: "0.4.0", build: "v0.4.0", publisherVersion: nil,
        socketPath: "/tmp/f.sock", flavour: .development)

    #expect(about.title.contains("development"))
}

@Test func aReleaseTitleIsJustTheName() {
    let about = About(
        appVersion: "0.4.0", build: "v0.4.0", publisherVersion: nil,
        socketPath: "/var/run/tun-manager.sock")

    #expect(about.title == "Tun Manager 0.4.0")
}

@Test func aboutShowsTheFingerprintOfTheKeyTheFeedAnnounced() {
    // The line somebody reads next to `sudo tun-manager feed-key`, to decide
    // whether the thing they are connected to is the thing on their machine.
    let about = About(
        appVersion: "0.6.0", build: "v0.6.0", publisherVersion: "v0.6.0",
        publisherKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg=",
        socketPath: "/var/run/tun-manager.sock")

    #expect(about.feedKey == "56:47:5a:a7:54:63:47:4c:02:85:df:5d:bf:2b:ca:b7")
    #expect(about.details.contains { $0.contains("56:47:5a") })
}

@Test func aboutNeverShowsTheKeyItself() {
    let key = "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="
    let about = About(
        appVersion: "0.6.0", build: "v0.6.0", publisherVersion: "v0.6.0",
        publisherKey: key, socketPath: "/var/run/tun-manager.sock")

    #expect(about.details.contains { $0.contains(key) } == false)
}

@Test func aPublisherWithNoKeySaysSoRatherThanShowingNothing() {
    // It means something: this publisher cannot be told apart from any other
    // program listening on that socket.
    let about = About(
        appVersion: "0.6.0", build: "v0.6.0", publisherVersion: "v0.6.0",
        publisherKey: nil, socketPath: "/var/run/tun-manager.sock")

    #expect(about.feedKey.contains("none"))
}

@Test func aKeyThatDoesNotDecodeIsCalledUnreadable() {
    // Not shown as a fingerprint of whatever the bytes were: that would invite
    // a comparison against tun-manager's and the wrong conclusion.
    let about = About(
        appVersion: "0.6.0", build: "v0.6.0", publisherVersion: "v0.6.0",
        publisherKey: "not a key", socketPath: "/var/run/tun-manager.sock")

    #expect(about.feedKey.contains("unreadable"))
}

@Test func withNoConnectionThereIsNoKeyToTalkAbout() {
    // Neither "none" nor "unreadable": nobody has said anything yet.
    let about = About(
        appVersion: "0.6.0", build: "v0.6.0", publisherVersion: nil,
        publisherKey: nil, socketPath: "/var/run/tun-manager.sock")

    #expect(about.feedKey == "not connected")
}

@Test func thePanelOffersToPostOneBecauseTheOthersAreSilentWhenTheyFail() {
    // Every notification this application sends on its own is silent when it
    // fails - permission refused, bundle not registered - so one asked for on
    // purpose is the only one whose absence means anything.
    let about = About(
        appVersion: "0.6.0", build: "v0.6.0", publisherVersion: nil, socketPath: "/x")

    #expect(about.testNotification == "Test Notification")
    #expect(about.notificationsRefused.contains("System Settings"))
}

@Test func theSampleIsKeptApartFromEveryTunnelsNotification() {
    // Tunnels are keyed under "tunnel.", so this can never replace one of
    // theirs - and asking twice replaces itself rather than stacking.
    let sample = NotificationBuilder.sample()

    #expect(sample.identifier == "test")
    #expect(sample.identifier.hasPrefix("tunnel.") == false)
    #expect(sample.body.contains("working"))
}
