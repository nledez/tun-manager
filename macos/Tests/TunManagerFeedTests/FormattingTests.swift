import Foundation
import Testing

@testable import TunManagerFeed

@Test func aByteCountOfZeroIsNothingRatherThanZero() {
    // The same choice the table makes: a tunnel that is down has no traffic to
    // report, and "0 bytes" reads like a measurement.
    #expect(Formatting.bytes(0) == "—")
}

@Test func aByteCountIsShownInTheUnitAPersonWouldUse() {
    let english = Locale(identifier: "en_US_POSIX")

    #expect(Formatting.bytes(184_320, locale: english) == "180 kB")
    #expect(Formatting.bytes(184_320_000, locale: english) == "175.8 MB")
}

@Test func aByteCountFollowsTheReadersLocaleRatherThanTheDevelopers() {
    // The menu is read by a person, so the separator and the unit belong to
    // them. This is also why every other assertion here pins a locale.
    let french = Locale(identifier: "fr_FR")

    #expect(Formatting.bytes(184_320_000, locale: french).contains("Mo"))
}

@Test func anAgeUnderAMinuteIsInSeconds() {
    #expect(Formatting.age(0) == "0s")
    #expect(Formatting.age(42) == "42s")
}

@Test func anAgeUnderAnHourIsZeroPaddedSoAColumnStaysAligned() {
    #expect(Formatting.age(64) == "1m04s")
    #expect(Formatting.age(3599) == "59m59s")
}

@Test func anAgeBeyondAnHourIsInHoursAndMinutes() {
    #expect(Formatting.age(3600) == "1h00m")
    #expect(Formatting.age(9240) == "2h34m")
}

@Test func aHandshakeInTheFutureReadsAsNowRatherThanAsGarbage() {
    // A clock adjustment is enough to produce one.
    #expect(Formatting.age(-30) == "0s")
}

@Test func theContextOmitsThePartsTheWireDidNotGive() {
    #expect(Formatting.context(FeedContext(name: "office")) == "office")
    #expect(
        Formatting.context(FeedContext(name: "office", interface: "en0", address: "198.51.100.42"))
            == "office — en0 · 198.51.100.42")
}

@Test func aMachineWithNoDetectedContextSaysSoRatherThanShowingAnEmptyLine() {
    #expect(Formatting.context(FeedContext(name: "")) == "no network context")
}
