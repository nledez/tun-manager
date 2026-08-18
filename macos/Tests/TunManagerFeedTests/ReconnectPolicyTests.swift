import Testing

@testable import TunManagerFeed

@Test func theFirstRetryAfterALostConnectionIsAQuarterOfASecond() {
    // Being dropped for falling behind recovers instantly, so guessing that
    // way costs one syscall when the guess is wrong.
    #expect(ReconnectPolicy.delay(after: .lost, attempt: 0) == .milliseconds(250))
}

@Test func theDelayDoublesOnEachAttempt() {
    #expect(ReconnectPolicy.delay(after: .lost, attempt: 1) == .milliseconds(500))
    #expect(ReconnectPolicy.delay(after: .lost, attempt: 2) == .seconds(1))
    #expect(ReconnectPolicy.delay(after: .lost, attempt: 3) == .seconds(2))
}

@Test func noDelayIsEverLongerThanThirtySecondsHoweverManyAttemptsHaveFailed() {
    for attempt in [8, 20, 1000, Int.max] {
        #expect(ReconnectPolicy.delay(after: .lost, attempt: attempt) == ReconnectPolicy.ceiling)
    }
}

@Test func aSocketThatIsNotThereIsProbedNoFasterThanOnceASecond() {
    // Four probes a second for a file that does not exist is noise and nothing
    // else: no amount of asking makes tun-manager start.
    #expect(ReconnectPolicy.delay(after: .notRunning, attempt: 0) == .seconds(1))
}

@Test func aSocketOwnedBySomebodyElseIsProbedNoFasterThanOnceEveryFiveSeconds() {
    #expect(ReconnectPolicy.delay(after: .notPermitted, attempt: 0) == .seconds(5))
}

@Test func aGoodbyeIsFollowedByTwoSecondsBecauseThatIsHowLongRestartingTakes() {
    #expect(ReconnectPolicy.delay(after: .goodbye, attempt: 0) == .seconds(2))
}

@Test func theSameReasonAndAttemptAlwaysGiveTheSameDelay() {
    // Pins the decision not to add jitter, so that a future "improvement" has
    // to argue with a red test. Jitter de-synchronises many clients from one
    // server; there is one of each here.
    for attempt in 0..<10 {
        let first = ReconnectPolicy.delay(after: .lost, attempt: attempt)
        #expect(ReconnectPolicy.delay(after: .lost, attempt: attempt) == first)
    }
}
