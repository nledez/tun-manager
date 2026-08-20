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
    /// Whether the publisher was named with `--socket`. Carried so every redraw
    /// says so, rather than only the first.
    private let demo: Bool
    private let notifications: NotificationPoster
    private let details: DetailWindowController
    private let flavour = Flavour(bundleIdentifier: Bundle.main.bundleIdentifier)
    private var model: MenuModel?
    /// Decides whether a refusal is news, so that the comparison is somewhere a
    /// test can reach it rather than here.
    private var prompt = WarningPrompt()

    init(
        supervisor: FeedSupervisor, socketPath: String, demo: Bool = false,
        notifications: NotificationPoster
    ) {
        self.supervisor = supervisor
        self.socketPath = socketPath
        self.demo = demo
        self.notifications = notifications
        self.details = DetailWindowController(supervisor: supervisor)
        super.init()

        // What makes the position stick when the user drags the item around,
        // and what remembers that they hid it. If they hid it, do not fight it.
        // Per flavour, or the installed application and the one being worked on
        // fight over the same remembered position.
        item.autosaveName = flavour.statusItemAutosaveName

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
        // The window follows the menu: a refusal empties both, or the table
        // would go on showing a proved session's tunnels under whatever is on
        // that socket now.
        details.refuse(model?.warning)
        // A round of probes arrives as a change like any other: the machine
        // keeps the results, and whoever draws reads them from it.
        details.refreshPings()
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
            state: supervisor.state, snapshot: supervisor.snapshot, now: Date(),
            socketPath: socketPath, demo: demo)
        announce(model?.warning)

        let image = NSImage(systemSymbolName: glyph.symbol, accessibilityDescription: glyph.description)
        image?.isTemplate = !flavour.isTinted
        var configuration = NSImage.SymbolConfiguration(pointSize: 15, weight: .regular)
        if flavour.isTinted {
            // Deliberately against the rule the release follows. With two
            // identical shields in the menu bar, colour is the only thing that
            // says which one is being looked at.
            configuration = configuration.applying(
                NSImage.SymbolConfiguration(paletteColors: [.systemPink]))
        }
        item.button?.image = image?.withSymbolConfiguration(configuration)
        item.button?.appearsDisabled = glyph.dimmed
        item.button?.toolTip = glyph.description

        if let menu = item.menu, menu.numberOfItems > 0 {
            rebuild(menu)
        }
    }

    private func rebuild(_ menu: NSMenu) {
        guard let model else { return }
        menu.removeAllItems()

        menu.addItem(disabled(model.headline, prominent: model.warning != nil))
        if let notice = model.demoNotice {
            // Under the headline and never removed: what is below it comes from
            // a publisher this application did not check for being root.
            menu.addItem(disabled(notice))
        }
        if model.warning != nil {
            // Right under the line it explains, and worded as a question rather
            // than as a warning: the line above is the warning, and this is the
            // way to what it does not have room to say.
            menu.addItem(action("Why Is This Refused?…", #selector(showWarning), key: ""))
        }
        if model.showsOverview {
            // At the top, above the tunnels: it is the way into the window
            // without picking a tunnel first, and picking one to get to the
            // table was a detour past something nobody asked to see.
            menu.addItem(.separator())
            let overview = action("All tunnels", #selector(showOverview), key: "")
            overview.image = NSImage(
                systemSymbolName: "list.bullet.rectangle", accessibilityDescription: nil)
            menu.addItem(overview)
        }
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
            // The terminal's key for the same thing, and bare rather than
            // command-p for the same reason it is bare there. A status menu's
            // key equivalents are only dispatched while it is open, so this
            // takes nothing away from any other application.
            let ping = action("Ping", #selector(self.ping), key: "p")
            ping.keyEquivalentModifierMask = []
            menu.addItem(ping)
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
            publisherKey: supervisor.publisherKey,
            socketPath: socketPath,
            flavour: flavour)
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
    @objc private func ping() { supervisor.askForPing() }
    @objc private func showDetail(_ sender: NSMenuItem) {
        guard let name = sender.representedObject as? String,
            let tunnels = supervisor.snapshot?.tunnels
        else { return }
        details.show(tunnel: name, tunnels: tunnels)
    }

    @objc private func showOverview() {
        details.show(tunnel: nil, tunnels: supervisor.snapshot?.tunnels ?? [])
    }

    /// Opens the panel by itself the first time a refusal appears.
    ///
    /// A menu bar item that quietly changes its glyph is a menu bar item nobody
    /// looks at, and this is the one thing this application knows that somebody
    /// has to be told rather than left to find. Once per refusal: the panel is
    /// offered from the menu afterwards.
    private func announce(_ warning: PublisherWarning?) {
        guard prompt.opens(for: warning), let warning else { return }
        // Out of the redraw rather than inside it: this is called from the
        // middle of the machine dispatching, and a modal run loop started there
        // would run the next event on top of the one still being handled.
        Task { @MainActor [weak self] in self?.present(warning) }
    }

    /// Shows the refusal, and does what the reply says.
    ///
    /// NOT TESTED: an NSAlert cannot be run in a suite, and there is nothing
    /// here to decide — the wording is PublisherWarning's, tested, and what the
    /// one button does is FeedSupervisor.forgetPinnedKey, tested.
    /// See macos/docs/coverage-gaps.md, "the menu bar target".
    private func present(_ warning: PublisherWarning) {
        let alert = NSAlert()
        alert.messageText = warning.title
        alert.informativeText = warning.details.joined(separator: "\n")
        // A warning rather than critical: critical is for what cannot be undone,
        // and nothing here has happened yet — the application is refusing, which
        // is the safe outcome already in force.
        alert.alertStyle = .warning
        // First is the default, and the default here is to keep refusing.
        // Trusting a key means every later connection is compared against it,
        // which is not a thing to do by pressing return on a panel that just
        // appeared.
        alert.addButton(withTitle: warning.dismiss)
        alert.addButton(withTitle: warning.accept)

        NSApplication.shared.activate()
        guard alert.runModal() == .alertSecondButtonReturn else { return }
        supervisor.forgetPinnedKey()
    }

    @objc private func showWarning() {
        guard let warning = model?.warning else { return }
        present(warning)
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
