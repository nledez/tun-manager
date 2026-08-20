import Testing

/// The tests that open real descriptors, under one roof.
///
/// Serialised here rather than suite by suite, because a trait on a suite
/// serialises its own tests and nothing else: three separately-serialised
/// suites still ran against each other, which is the case that matters. A
/// hundred tests creating and tearing down Dispatch channels at once trips
/// libdispatch's own assertion about a descriptor going away under an active
/// channel, and closed descriptor numbers come straight back out of the next
/// socket(2) in the process — so one test's teardown lands on another test's
/// listener, which then refuses every connection for no reason visible from
/// there.
///
/// A property of the harness, not of the program: in use there is one
/// connection at a time, opened and closed by the supervisor on the main actor.
@Suite(.serialized)
struct RealSockets {}
