import TunManagerFeed
import UserNotifications
import os

// NOT TESTED: hands already-decided notifications to the system. What is said,
// and when, is decided by NotificationBuilder in the library and tested there.
// See macos/docs/coverage-gaps.md, "the menu bar target".

/// Posts notifications, once somebody has agreed to receive them.
@MainActor
final class NotificationPoster {
    private let log = Logger(subsystem: "net.ledez.tun-manager", category: "notifications")
    private var allowed = false

    /// Whether the system will show anything at all. Read by the About panel,
    /// which offers to post one and has to be able to say when posting it
    /// would be doing nothing.
    var isAllowed: Bool { allowed }

    /// Asks once, at launch. A refusal is remembered by the system, so asking
    /// again on every start would be nagging rather than persistence.
    func requestPermission() {
        log.notice("asking for notification permission")
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) {
            [weak self] granted, error in
            if let error {
                // Most often: the process has no bundle identifier because it
                // was launched by `swift run` rather than as an application.
                Task { @MainActor in self?.log.error("notifications unavailable: \(error)") }
                return
            }
            Task { @MainActor in
                self?.log.notice("notification permission granted: \(granted, privacy: .public)")
                self?.allowed = granted
            }
        }
    }

    func post(_ requests: [NotificationRequest]) {
        guard !requests.isEmpty else { return }
        guard allowed else {
            // Worth saying: "why did I not get a banner" is otherwise
            // unanswerable from outside.
            log.notice("\(requests.count) notification(s) not posted: permission was refused")
            return
        }
        log.notice("posting \(requests.count) notification(s)")
        for request in requests {
            let content = UNMutableNotificationContent()
            content.title = request.title
            content.body = request.body
            // No attachment and no icon: a notification from a signed bundle
            // already carries that bundle's icon. This is the whole reason the
            // Go side's -contentImage workaround can eventually go away.
            UNUserNotificationCenter.current().add(
                UNNotificationRequest(
                    identifier: request.identifier, content: content, trigger: nil))
        }
    }
}
