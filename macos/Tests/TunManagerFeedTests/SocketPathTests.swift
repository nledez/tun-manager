import Foundation
import Testing

@testable import TunManagerFeed

/// Defaults nobody else has written to.
private func emptyDefaults() -> UserDefaults {
    UserDefaults(suiteName: "socket-path-\(UUID().uuidString)")!
}

@Test func withoutASettingTheSocketIsWhereThePublisherPutsItByDefault() {
    #expect(
        SocketPath.resolved(arguments: ["tun-manager-menubar"], defaults: emptyDefaults())
            == "/var/run/tun-manager.sock")
}

@Test func aConfiguredSocketPathIsUsedInstead() {
    // feed_socket can move the socket on the publisher's side, and an app that
    // ignored that would say "not running" forever with no explanation.
    let defaults = emptyDefaults()
    defaults.set("/tmp/elsewhere.sock", forKey: SocketPath.defaultsKey)

    #expect(
        SocketPath.resolved(arguments: ["tun-manager-menubar"], defaults: defaults)
            == "/tmp/elsewhere.sock")
}

@Test func theFlagNamesTheSocketToConnectTo() {
    #expect(
        SocketPath.resolved(
            arguments: ["tun-manager-menubar", "--socket", "/tmp/tm-demo/feed.sock"],
            defaults: emptyDefaults()) == "/tmp/tm-demo/feed.sock")
}

@Test func theFlagIsAlsoAcceptedWithAnEqualsSign() {
    #expect(
        SocketPath.resolved(
            arguments: ["tun-manager-menubar", "--socket=/tmp/tm-demo/feed.sock"],
            defaults: emptyDefaults()) == "/tmp/tm-demo/feed.sock")
}

@Test func theFlagBeatsAStoredSetting() {
    // A `defaults write` persists until somebody remembers to delete it, and a
    // key left pointing at a demo publisher is a menu bar quietly listing
    // tunnels that do not exist. The flag leaves nothing behind, so it wins.
    let defaults = emptyDefaults()
    defaults.set("/tmp/stale.sock", forKey: SocketPath.defaultsKey)

    #expect(
        SocketPath.resolved(
            arguments: ["tun-manager-menubar", "--socket", "/tmp/fresh.sock"],
            defaults: defaults) == "/tmp/fresh.sock")
}

@Test func aFlagWithNothingAfterItNamesNothing() {
    // "--socket" as the last argument. Reading it as an empty path would
    // produce a connect to "" rather than a fall back to the default.
    #expect(
        SocketPath.resolved(arguments: ["tun-manager-menubar", "--socket"], defaults: emptyDefaults())
            == "/var/run/tun-manager.sock")
}

@Test func anArgumentThatOnlyLooksLikeTheFlagIsNotOne() {
    #expect(
        SocketPath.resolved(
            arguments: ["tun-manager-menubar", "--socketed", "/tmp/no.sock"],
            defaults: emptyDefaults()) == "/var/run/tun-manager.sock")
}

@Test func theProgramsOwnPathIsNeverMistakenForTheFlag() {
    // argv[0] is whatever the binary was invoked as, which on a bundle is a
    // long path nobody chose.
    #expect(
        SocketPath.resolved(arguments: ["--socket"], defaults: emptyDefaults())
            == "/var/run/tun-manager.sock")
}
