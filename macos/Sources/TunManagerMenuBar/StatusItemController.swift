import AppKit
import TunManagerFeed

/// Owns the menu bar item and its menu.
@MainActor
final class StatusItemController: NSObject, FeedObserver, NSMenuDelegate {
    // Held in a stored property: a status item that goes out of scope is
    // removed from the menu bar, silently, exactly like the delegate.
    private let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    private let supervisor: FeedSupervisor
    private let socketPath: String
    private let notifications: NotificationPoster
    private let details: DetailWindowController
    private var model: MenuModel?

    init(supervisor: FeedSupervisor, socketPath: String, notifications: NotificationPoster) {
        self.supervisor = supervisor
        self.socketPath = socketPath
        self.notifications = notifications
        self.details = DetailWindowController(supervisor: supervisor)
        super.init()

        // What makes the position stick when the user drags the item around,
        // and what remembers that they hid it. If they hid it, do not fight it.
        item.autosaveName = "net.ledez.tun-manager.menubar.status"

        let menu = NSMenu()
        // Otherwise AppKit decides enablement itself from whether an item has a
        // target, and overrules the attributed titles above.
        menu.autoenablesItems = false
        menu.delegate = self
        item.menu = menu

        redraw()
    }

    // MARK: - FeedObserver

    func linkDidChange(state: LinkState, snapshot: Snapshot?, publisherVersion: String?) {
        redraw()
    }

    func linkDidPublish(snapshot: Snapshot, diff: SnapshotDiff) {
        notifications.post(NotificationBuilder.requests(for: diff))
        details.update(tunnels: snapshot.tunnels)
    }

    func linkDidSample(_ sample: Sample) {
        details.add(sample)
    }

    // MARK: - NSMenuDelegate

    func menuNeedsUpdate(_ menu: NSMenu) {
        rebuild(menu)
    }

    func menuWillOpen(_ menu: NSMenu) {
        // The default refresh interval is five minutes. Asking at the moment
        // somebody looks is what stops the menu quietly showing stale health;
        // the publisher caps these at one every two seconds.
        supervisor.menuWillOpen()
    }

    // MARK: - Drawing

    private func redraw() {
        let glyph = StatusGlyph.of(state: supervisor.state, snapshot: supervisor.snapshot)
        model = MenuModelBuilder.build(
            state: supervisor.state, snapshot: supervisor.snapshot, now: Date())

        let image = NSImage(systemSymbolName: glyph.symbol, accessibilityDescription: glyph.description)
        image?.isTemplate = true
        item.button?.image = image?.withSymbolConfiguration(
            NSImage.SymbolConfiguration(pointSize: 15, weight: .regular))
        item.button?.appearsDisabled = glyph.dimmed
        item.button?.toolTip = glyph.description

        if let menu = item.menu, menu.numberOfItems > 0 {
            rebuild(menu)
        }
    }

    private func rebuild(_ menu: NSMenu) {
        guard let model else { return }
        menu.removeAllItems()

        menu.addItem(disabled(model.headline))
        for section in model.sections {
            menu.addItem(.separator())
            if let header = section.header {
                menu.addItem(disabled(header))
            }
            for row in section.rows {
                menu.addItem(entry(row))
            }
        }

        menu.addItem(.separator())
        menu.addItem(disabled(model.footnote))
        if model.canRefresh {
            menu.addItem(action("Refresh", #selector(refresh), key: "r"))
        } else {
            menu.addItem(action("Try Again", #selector(retry), key: "r"))
        }

        menu.addItem(.separator())
        menu.addItem(action("About Tun Manager…", #selector(showAbout), key: ""))
        menu.addItem(action("Quit Tun Manager", #selector(quit), key: "q"))
    }

    /// Reads what `make app` stamped into the bundle, so a running application
    /// can say exactly which build it is.
    private var about: About {
        let info = Bundle.main.infoDictionary ?? [:]
        return About(
            appVersion: info["CFBundleShortVersionString"] as? String ?? "unknown",
            build: info["NETLedezGitDescribe"] as? String ?? "unknown",
            publisherVersion: supervisor.publisherVersion,
            socketPath: socketPath)
    }

    /// A tunnel row. Clicking it opens the window rather than a submenu: the
    /// facts fit in a menu, but what is going through the tunnel does not.
    private func entry(_ row: MenuModel.Row) -> NSMenuItem {
        let entry = NSMenuItem(title: row.title, action: #selector(showDetail(_:)), keyEquivalent: "")
        entry.target = self
        entry.image = NSImage(systemSymbolName: row.symbol, accessibilityDescription: nil)
        // Carried on the item so the handler knows which row was clicked
        // without the controller keeping a parallel list to index into.
        entry.representedObject = row.title
        return entry
    }

    private func disabled(_ title: String, prominent: Bool = false) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        item.attributedTitle = NSAttributedString(
            string: title,
            attributes: [
                .foregroundColor: prominent ? NSColor.labelColor : NSColor.secondaryLabelColor,
                // Monospaced digits so a column of byte counts and ages lines
                // up instead of drifting with the width of each numeral.
                .font: NSFont.monospacedDigitSystemFont(
                    ofSize: NSFont.systemFontSize, weight: .regular),
            ])
        return item
    }

    private func action(_ title: String, _ selector: Selector, key: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: selector, keyEquivalent: key)
        item.target = self
        return item
    }

    @objc private func refresh() { supervisor.menuWillOpen() }
    @objc private func retry() { supervisor.userAskedToRetry() }
    @objc private func showDetail(_ sender: NSMenuItem) {
        guard let name = sender.representedObject as? String,
            let tunnels = supervisor.snapshot?.tunnels
        else { return }
        details.show(tunnel: name, tunnels: tunnels)
    }

    @objc private func showAbout() {
        let about = self.about
        let alert = NSAlert()
        alert.messageText = about.title
        alert.informativeText = about.details.joined(separator: "\n")
        alert.alertStyle = .informational
        alert.addButton(withTitle: "OK")
        // An accessory application has nothing frontmost, so without this the
        // panel opens behind whatever the user is actually looking at.
        NSApplication.shared.activate()
        alert.runModal()
    }

    @objc private func quit() { NSApplication.shared.terminate(nil) }
}
