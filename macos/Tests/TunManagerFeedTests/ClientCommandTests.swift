import Foundation
import Testing

@testable import TunManagerFeed

@Test func refreshIsWrittenAsTheOneLineThePublisherExpects() {
    let got = String(decoding: ClientCommand.refresh.line, as: UTF8.self)

    #expect(got == "{\"type\":\"refresh\"}\n")
}

@Test func everyCommandEndsWithANewlineBecauseThatIsTheFraming() {
    for command in ClientCommand.allCases {
        #expect(command.line.last == UInt8(ascii: "\n"), "\(command) is not newline-terminated")
    }
}
