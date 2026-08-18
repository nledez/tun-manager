import Foundation

/// One measured pair of rates, in bytes per second.
public struct Rate: Sendable, Equatable {
    public let down: Double
    public let up: Double
}

/// Turns the feed's cumulative counters into a bounded history of rates.
///
/// The counters only ever say how much has passed since the tunnel came up, so
/// a rate is a difference between two readings. Three things make that
/// difference lie, and all three are handled here rather than by whatever draws
/// it: the first reading has nothing to compare against, a tunnel restarted
/// between two readings starts counting from zero again, and two readings taken
/// at the same instant would divide by no time at all.
///
/// The same reasoning as internal/rate on the Go side, which the terminal's
/// graph is built on — deliberately, so the two agree about what a spike means.
public struct RateSeries {
    private struct Reading {
        let at: Date
        let rx: Int64
        let tx: Int64
    }

    private let limit: Int
    private var history: [Rate] = []
    private var last: Reading?

    public init(limit: Int) {
        self.limit = limit
    }

    public var points: [Rate] { history }
    public var peakDown: Double { history.map(\.down).max() ?? 0 }
    public var peakUp: Double { history.map(\.up).max() ?? 0 }

    /// Records a reading, and the rate between it and the one before it.
    public mutating func add(at instant: Date, rx: Int64, tx: Int64) {
        defer { last = Reading(at: instant, rx: rx, tx: tx) }
        guard let previous = last else { return }

        let elapsed = instant.timeIntervalSince(previous.at)
        // Either the clock did not move or it went backwards. Neither is a rate.
        guard elapsed > 0 else { return }

        push(
            Rate(
                down: perSecond(rx - previous.rx, over: elapsed),
                up: perSecond(tx - previous.tx, over: elapsed)))
    }

    /// Forgets everything, including the last reading.
    ///
    /// Dropping the reading matters as much as dropping the history: keeping it
    /// across a change of tunnel would difference one tunnel's counters against
    /// another's and draw a spike that never happened.
    public mutating func clear() {
        history.removeAll(keepingCapacity: true)
        last = nil
    }

    /// A counter that went backwards means the tunnel was restarted between the
    /// two readings, not that bytes travelled the other way.
    private func perSecond(_ delta: Int64, over elapsed: TimeInterval) -> Double {
        delta < 0 ? 0 : Double(delta) / elapsed
    }

    private mutating func push(_ rate: Rate) {
        guard limit > 0 else { return }
        history.append(rate)
        if history.count > limit {
            history.removeFirst(history.count - limit)
        }
    }
}
