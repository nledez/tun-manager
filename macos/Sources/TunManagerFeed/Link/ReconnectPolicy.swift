/// How long to wait before trying the feed again.
///
/// Pure and without randomness. Jitter exists to de-synchronise many clients
/// from one server, and there is exactly one of each here — adding it would
/// mean injecting a random source purely so the tests could be deterministic,
/// and a test asserting `delay == 4s` is worth more than one asserting
/// `delay <= 4s`.
public enum ReconnectPolicy {
    /// The worst case between somebody typing `sudo tun-manager` and the menu
    /// bar noticing. Only defensible because opening the menu, waking the
    /// machine, and the manual item all short-circuit it.
    public static let ceiling = Duration.seconds(30)

    public static func delay(after reason: Disconnection, attempt: Int) -> Duration {
        let base: Duration =
            switch reason {
            // Recovering from having been dropped for falling behind should be
            // imperceptible, and a unix connect(2) costs a path lookup.
            case .lost, .rejected, .failed: .milliseconds(250)
            // The socket is absent, and no amount of probing makes a file
            // appear faster.
            case .notRunning, .refused: .seconds(1)
            // Only a human restarting tun-manager under sudo fixes this.
            case .notPermitted: .seconds(5)
            // Nothing to hurry for: either somebody is standing in for
            // tun-manager, in which case asking faster gains nothing, or a demo
            // publisher is still holding the path and will be told to stop by a
            // person. Slow enough to leave the reason on screen long enough to
            // be read.
            case .notRoot: .seconds(5)
            // `^C`, edit a config, start it again — the common human sequence.
            // Faster than the ladder, slower than pointless.
            case .goodbye: .seconds(2)
            }

        // Doubling is capped before it is applied: 1 << 60 is not a number this
        // program should ever construct, whatever the attempt counter says.
        return min(ceiling, base * (1 << min(attempt, 8)))
    }
}
