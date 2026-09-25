import AppKit
import ServiceManagement

@main
struct CullSyncMain {
    @MainActor static func main() {
        let app = NSApplication.shared
        let delegate = AppDelegate()
        app.delegate = delegate
        // A menu-bar helper: no Dock icon and no windows of its own.
        app.setActivationPolicy(.accessory)
        withExtendedLifetime(delegate) { app.run() }
    }
}

/// The menu-bar item. It shows what the helper is doing and offers the few
/// things a person needs; the actual decisions are all made in the browser.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private var item: NSStatusItem?
    private var agent: Agent?
    private var snapshot = AgentSnapshot()
    private var loginProblem: String?
    /// Set by the LaunchAgent the setup command installs. launchd then starts
    /// the app at login and restarts it after a crash, so the app does not
    /// offer a login item of its own as well.
    private let launchAgent = ProcessInfo.processInfo.environment["CULL_SYNC_LAUNCH_AGENT"] == "1"

    func applicationDidFinishLaunching(_ notification: Notification) {
        // A login item left from before the LaunchAgent would start a second
        // copy at every login.
        if launchAgent, SMAppService.mainApp.status == .enabled {
            try? SMAppService.mainApp.unregister()
        }
        // One copy is enough: two would only race each other for work. This one
        // quits cleanly, so launchd does not start it again.
        let me = ProcessInfo.processInfo.processIdentifier
        if NSRunningApplication.runningApplications(withBundleIdentifier: Bundle.main.bundleIdentifier ?? "")
            .contains(where: { $0.processIdentifier != me }) {
            NSApp.terminate(nil)
            return
        }
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        self.item = item
        render()
        let agent = Agent { [weak self] next in self?.snapshot = next; self?.render() }
        self.agent = agent
        Task { await agent.start() }
    }

    private func render() {
        guard let item else { return }
        let symbol = snapshot.activity != nil ? "arrow.triangle.2.circlepath"
            : snapshot.connection == .connected ? "photo.on.rectangle.angled" : "photo.badge.exclamationmark"
        let image = NSImage(systemSymbolName: symbol, accessibilityDescription: "Cull Sync")
            ?? NSImage(systemSymbolName: "photo", accessibilityDescription: "Cull Sync")
        image?.isTemplate = true
        item.button?.image = image
        item.button?.toolTip = "Cull Sync: \(connectionText)"
        item.menu = menu()
    }

    private var connectionText: String {
        switch snapshot.connection {
        case .starting: "Connecting to Daddy Cull…"
        case .connected: "Connected to Daddy Cull"
        case .unreachable: "Can't reach Daddy Cull"
        case .needsKey: "No key in sync.conf"
        case .refused: "Key refused: set up again from the Apple Photos page"
        case .disabled: "Daddy Cull has no PHOTOS_AGENT_KEY set"
        }
    }

    private func menu() -> NSMenu {
        let menu = NSMenu()
        menu.autoenablesItems = false
        func line(_ title: String) {
            let entry = NSMenuItem(title: title, action: nil, keyEquivalent: "")
            entry.isEnabled = false
            menu.addItem(entry)
        }
        line(connectionText)
        if snapshot.connection == .unreachable || snapshot.connection == .starting {
            line(snapshot.serverURL)
        }
        if let activity = snapshot.activity { line(activity) }
        if let last = snapshot.lastSync {
            let summary = snapshot.lastSyncSummary.map { " · \($0)" } ?? ""
            line("Last sync \(last.formatted(date: .abbreviated, time: .shortened))\(summary)")
        } else {
            line("Not synced yet")
        }
        if snapshot.access != "authorized" {
            line(snapshot.access == "limited" ? "Photos access is limited" : "No access to Photos")
            menu.addItem(action("Open Photos Privacy Settings…", #selector(openPrivacy)))
        }
        if [.needsKey, .refused].contains(snapshot.connection) {
            menu.addItem(action("Show sync.conf in Finder", #selector(revealConfig)))
        }
        menu.addItem(.separator())
        menu.addItem(action("Open Daddy Cull", #selector(openCull)))
        if launchAgent {
            line("Starts at login")
        } else {
            let login = action("Start at Login", #selector(toggleLogin))
            switch SMAppService.mainApp.status {
            case .enabled: login.state = .on
            case .requiresApproval: login.state = .mixed
            default: login.state = .off
            }
            menu.addItem(login)
            if let loginProblem { line(loginProblem) }
        }
        menu.addItem(.separator())
        menu.addItem(action("Quit Cull Sync", #selector(NSApplication.terminate(_:)), key: "q"))
        return menu
    }

    private func action(_ title: String, _ selector: Selector, key: String = "") -> NSMenuItem {
        let entry = NSMenuItem(title: title, action: selector, keyEquivalent: key)
        entry.target = selector == #selector(NSApplication.terminate(_:)) ? NSApp : self
        return entry
    }

    @objc private func openCull() {
        if let url = URL(string: "\(snapshot.serverURL)/photos") { NSWorkspace.shared.open(url) }
    }

    @objc private func openPrivacy() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Photos") {
            NSWorkspace.shared.open(url)
        }
    }

    @objc private func revealConfig() {
        NSWorkspace.shared.activateFileViewerSelecting([SyncConfig.fileURL])
    }

    /// Registers the app itself as a login item. macOS may want the person to
    /// approve that in System Settings, in which case it is opened for them.
    @objc private func toggleLogin() {
        let service = SMAppService.mainApp
        loginProblem = nil
        do {
            if service.status == .enabled {
                try service.unregister()
            } else {
                try service.register()
                if service.status == .requiresApproval { SMAppService.openSystemSettingsLoginItems() }
            }
        } catch {
            loginProblem = "Could not change Start at Login: \(error.localizedDescription)"
        }
        render()
    }
}
