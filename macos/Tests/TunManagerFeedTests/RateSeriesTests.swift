import Foundation
import Testing

@testable import TunManagerFeed

private let t0 = Date(timeIntervalSince1970: 1_786_968_191)

@Test func theFirstReadingHasNoRate() {
    // A rate is a difference between two readings, and there is only one.
    var series = RateSeries(limit: 10)

    series.add(at: t0, rx: 1000, tx: 500)

    #expect(series.points.isEmpty)
}

@Test func aRateIsTheDifferenceOverTheElapsedTime() {
    var series = RateSeries(limit: 10)
    series.add(at: t0, rx: 1000, tx: 500)

    series.add(at: t0.addingTimeInterval(2), rx: 3000, tx: 900)

    #expect(series.points.count == 1)
    #expect(series.points[0].down == 1000)
    #expect(series.points[0].up == 200)
}

@Test func aCounterGoingBackwardsReadsAsIdle() {
    // A tunnel taken down and brought back up starts counting again. The
    // difference is negative, which as a rate would be a spike the wrong way.
    var series = RateSeries(limit: 10)
    series.add(at: t0, rx: 5000, tx: 5000)

    series.add(at: t0.addingTimeInterval(1), rx: 10, tx: 10)

    #expect(series.points[0].down == 0)
    #expect(series.points[0].up == 0)
}

@Test func twoReadingsAtTheSameInstantAreIgnored() {
    // Dividing by no elapsed time is an infinite rate.
    var series = RateSeries(limit: 10)
    series.add(at: t0, rx: 1000, tx: 500)

    series.add(at: t0, rx: 9999, tx: 9999)

    #expect(series.points.isEmpty)
}

@Test func aReadingFromThePastIsIgnored() {
    var series = RateSeries(limit: 10)
    series.add(at: t0, rx: 1000, tx: 500)

    series.add(at: t0.addingTimeInterval(-1), rx: 2000, tx: 900)

    #expect(series.points.isEmpty)
}

@Test func theSeriesKeepsOnlyItsMostRecentPoints() {
    // The window is what fits on screen; older than that is off the left edge.
    var series = RateSeries(limit: 3)

    for i in 0...5 {
        series.add(at: t0.addingTimeInterval(Double(i)), rx: Int64(i * 100), tx: 0)
    }

    #expect(series.points.count == 3)
    #expect(series.points.allSatisfy { $0.down == 100 })
}

@Test func peakReportsTheHighestOfEachDirection() {
    // The two directions scale on their own, or a download worth watching
    // flattens the upload into a line along the axis.
    var series = RateSeries(limit: 10)
    series.add(at: t0, rx: 0, tx: 0)
    series.add(at: t0.addingTimeInterval(1), rx: 100, tx: 900)
    series.add(at: t0.addingTimeInterval(2), rx: 900, tx: 1000)

    #expect(series.peakDown == 800)
    #expect(series.peakUp == 900)
}

@Test func anEmptySeriesHasNoPeak() {
    let series = RateSeries(limit: 10)

    #expect(series.peakDown == 0)
    #expect(series.peakUp == 0)
}

@Test func aSeriesForgetsWhatItWasWatchingWhenItIsCleared() {
    // Switching tunnels must not join the end of one tunnel's history to the
    // start of another's, which would draw a spike that never happened.
    var series = RateSeries(limit: 10)
    series.add(at: t0, rx: 1_000_000, tx: 0)
    series.add(at: t0.addingTimeInterval(1), rx: 1_000_100, tx: 0)

    series.clear()
    series.add(at: t0.addingTimeInterval(2), rx: 5, tx: 0)
    series.add(at: t0.addingTimeInterval(3), rx: 10, tx: 0)

    #expect(series.points.count == 1)
    #expect(series.points[0].down == 5)
}
