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
        let path = SocketPath.resolved()
        let supervisor = FeedSupervisor(transport: UnixSocketTransport(path: path))
        let statusItem = StatusItemController(
            supervisor: supervisor, socketPath: path, notifications: notifications)

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
