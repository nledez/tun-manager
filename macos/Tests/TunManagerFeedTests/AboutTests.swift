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
