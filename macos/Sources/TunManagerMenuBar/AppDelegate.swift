import AppKit
import TunManagerFeed

/// The composition root: builds the transport, the supervisor and the status
/// item, and wires them to each other.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private var supervisor: FeedSupervisor?
    private var statusItem: StatusItemController?
    private let notifications = NotificationPoster()

    func applicationDidFinishLaunching(_ notification: Notification) {
        // The choice, not just the path: a socket named with --socket is a demo
        // publisher, which does not run as root - and that is the only reason
        // it is safe to talk to. Everything else has to be root's.
        let choice = SocketPath.chosen()
        let path = choice.path
        let supervisor = FeedSupervisor(
            transport: UnixSocketTransport(path: path, policy: PeerPolicy.of(choice)),
            socketPath: path)
        let statusItem = StatusItemController(
            supervisor: supervisor, socketPath: path, demo: choice.isDemo,
            notifications: notifications)

        supervisor.observer = statusItem
        self.supervisor = supervisor
        self.statusItem = statusItem

        // Waking is one of the three things that short-circuits the retry
        // ladder, and the reason a thirty-second ceiling is defensible.
        NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didWakeNotification, object: nil, queue: .main
        ) { [weak supervisor] _ in
            MainActor.assumeIsolated { supervisor?.systemDidWake() }
        }

        notifications.requestPermission()
        supervisor.start()
    }

    /// Closing the window leaves the application in the menu bar, which is
    /// where it lives. Quitting is the Quit item, and nothing else.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    func applicationWillTerminate(_ notification: Notification) {
        supervisor?.stop()
    }
}
