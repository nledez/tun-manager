import Foundation
import Testing

@testable import TunManagerFeed

// The publisher writes two different shapes on the same wire: `taken` goes
// through Go's time.Time marshaller, which prints as many fractional digits as
// it has and none when it has none, while `last_handshake` is pre-formatted
// with time.RFC3339 and never carries one. These tests pin both, and they are
// the guard against Foundation's tolerance changing under us.

/// 2026-08-17T14:03:11+02:00, the instant the samples below name.
private let instant = Date(timeIntervalSince1970: 1_786_968_191)

@Test func aTakenTimestampWithNineFractionalDigitsDecodesToTheSecondItNames() {
    let got = RFC3339.date(from: "2026-08-17T14:03:11.123456789+02:00")

    #expect(got != nil)
    #expect(abs(got!.timeIntervalSince(instant) - 0.123456789) < 0.000_001)
}

@Test func aTakenTimestampWithOneFractionalDigitDecodesBecauseGoStripsTrailingZeros() {
    let got = RFC3339.date(from: "2026-08-17T14:03:11.1+02:00")

    #expect(got != nil)
    #expect(abs(got!.timeIntervalSince(instant) - 0.1) < 0.000_001)
}

@Test func aTimestampWhoseNanosecondsWereZeroHasNoFractionAndStillDecodes() {
    #expect(RFC3339.date(from: "2026-08-17T14:03:11+02:00") == instant)
}

@Test func aLastHandshakeWithNoFractionalPartDecodes() {
    #expect(RFC3339.date(from: "2026-08-17T14:03:11+02:00") == instant)
}

@Test func theSameInstantWrittenInZAndInAnOffsetDecodesToTheSameDate() {
    #expect(RFC3339.date(from: "2026-08-17T12:03:11Z") == instant)
}

@Test func aStringThatIsNotATimestampIsRefusedRatherThanBecoming1970() {
    for text in ["", "yesterday", "2026-08-17", "1786968191"] {
        #expect(RFC3339.date(from: text) == nil, "\(text) was accepted")
    }
}
