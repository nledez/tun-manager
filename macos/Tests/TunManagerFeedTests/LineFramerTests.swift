import Foundation
import Testing

@testable import TunManagerFeed

private func chunk(_ text: String) -> Data { Data(text.utf8) }
private func text(_ lines: [Data]) -> [String] { lines.map { String(decoding: $0, as: UTF8.self) } }

@Test func aLineSplitAcrossTwoChunksIsDeliveredOnceWhole() {
    var framer = LineFramer()

    #expect(framer.push(chunk(#"{"type":"he"#)).isEmpty)
    let got = framer.push(chunk("llo\"}\n"))

    #expect(text(got) == [#"{"type":"hello"}"#])
}

@Test func twoLinesArrivingInOneChunkAreDeliveredAsTwoInOrder() {
    var framer = LineFramer()

    let got = framer.push(chunk("first\nsecond\n"))

    #expect(text(got) == ["first", "second"])
}

@Test func aChunkEndingExactlyOnANewlineLeavesNothingBuffered() {
    var framer = LineFramer()
    _ = framer.push(chunk("first\n"))

    #expect(framer.push(chunk("second\n")).count == 1)
}

@Test func aTrailingPartialLineIsWithheldUntilItsNewlineArrives() {
    var framer = LineFramer()

    #expect(text(framer.push(chunk("whole\npart"))) == ["whole"])
    #expect(text(framer.push(chunk("ial\n"))) == ["partial"])
}

@Test func anEmptyLineIsDroppedRatherThanHandedToTheDecoder() {
    var framer = LineFramer()

    #expect(text(framer.push(chunk("\n\nreal\n"))) == ["real"])
}

@Test func aLineLongerThanTheCapIsDroppedAndFramingResumesAtTheNextNewline() {
    // A publisher that lost its mind, or bytes that are not this protocol at
    // all. Buffering without a bound is how a client runs a machine out of
    // memory on behalf of a process it does not control.
    var framer = LineFramer(limit: 16)

    #expect(framer.push(chunk(String(repeating: "x", count: 64))).isEmpty)
    let got = framer.push(chunk("still rubbish\nsane\n"))

    #expect(text(got) == ["sane"])
}
