import Foundation
import Testing

@testable import TunManagerFeed

private func text(_ command: ClientCommand) -> String {
    String(decoding: command.line, as: UTF8.self)
}

/// Decodes a command back into its fields.
///
/// The assertions go through the JSON rather than through the bytes: field
/// order carries no meaning in JSON, the publisher unmarshals into a struct,
/// and pinning the order would pin something neither side promises.
private func fields(_ command: ClientCommand) throws -> [String: String] {
    let object = try JSONSerialization.jsonObject(with: command.line)
    return (object as? [String: String]) ?? [:]
}

@Test func refreshCarriesNothingButItsType() throws {
    #expect(try fields(.refresh) == ["type": "refresh"])
}

@Test func watchNamesTheTunnelItWants() throws {
    #expect(try fields(.watch("alpha")) == ["type": "watch", "tunnel": "alpha"])
}

@Test func unwatchNamesTheTunnelItIsDoneWith() throws {
    #expect(try fields(.unwatch("alpha")) == ["type": "unwatch", "tunnel": "alpha"])
}

@Test func everyCommandEndsWithANewlineBecauseThatIsTheFraming() {
    for command in [ClientCommand.refresh, .watch("alpha"), .unwatch("alpha")] {
        #expect(text(command).hasSuffix("\n"), "\(command) is not newline-terminated")
    }
}

@Test func aTunnelNameIsEscapedRatherThanPastedIn() throws {
    // Tunnel names come from file names on somebody else's disk. Building JSON
    // by concatenation is how one containing a quote becomes a broken line, or
    // worse, a second field.
    #expect(try fields(.watch("od\"d"))["tunnel"] == "od\"d")
}
