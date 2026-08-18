import Foundation
import Testing

@testable import TunManagerFeed

@Test func withoutASettingTheSocketIsWhereThePublisherPutsItByDefault() {
    let defaults = UserDefaults(suiteName: "socket-path-empty-\(UUID().uuidString)")!

    #expect(SocketPath.resolved(defaults) == "/var/run/tun-manager.sock")
}

@Test func aConfiguredSocketPathIsUsedInstead() {
    // feed_socket can move the socket on the publisher's side, and an app that
    // ignored that would say "not running" forever with no explanation.
    let defaults = UserDefaults(suiteName: "socket-path-set-\(UUID().uuidString)")!
    defaults.set("/tmp/elsewhere.sock", forKey: SocketPath.defaultsKey)

    #expect(SocketPath.resolved(defaults) == "/tmp/elsewhere.sock")
}
