import AppKit
import Foundation
import WebKit

final class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate, WKUIDelegate {
    private var window: NSWindow?
    private var webView: WKWebView?
    private var serverProcess: Process?

    private let dashboardURL = URL(string: "http://127.0.0.1:8787")!

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        startServerIfNeeded()
        createWindow()
        waitForServerAndLoad()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    func applicationWillTerminate(_ notification: Notification) {
        if let process = serverProcess, process.isRunning {
            process.terminate()
            DispatchQueue.global().async {
                Thread.sleep(forTimeInterval: 1.0)
                if process.isRunning {
                    process.interrupt()
                }
            }
        }
    }

    private func createWindow() {
        let configuration = WKWebViewConfiguration()
        configuration.defaultWebpagePreferences.allowsContentJavaScript = true
        let webView = WKWebView(frame: .zero, configuration: configuration)
        webView.navigationDelegate = self
        webView.uiDelegate = self
        self.webView = webView

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1180, height: 760),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "Nucleus"
        window.center()
        window.contentView = webView
        window.makeKeyAndOrderFront(nil)
        self.window = window
        NSApp.activate(ignoringOtherApps: true)
    }

    private func startServerIfNeeded() {
        if serverIsReachable() {
            return
        }
        guard let binary = Bundle.main.url(forResource: "nucleus", withExtension: nil) else {
            showError("Bundled nucleus server binary is missing.")
            return
        }
        let logs = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/Nucleus", isDirectory: true)
        try? FileManager.default.createDirectory(at: logs, withIntermediateDirectories: true)
        let logFile = logs.appendingPathComponent("nucleus.log")

        let process = Process()
        process.executableURL = binary
        process.arguments = ["serve"]
        if let handle = try? FileHandle(forWritingTo: logFile) {
            handle.seekToEndOfFile()
            process.standardOutput = handle
            process.standardError = handle
        }
        do {
            try process.run()
            serverProcess = process
        } catch {
            showError("Failed to start Nucleus server: \(error.localizedDescription)")
        }
    }

    private func waitForServerAndLoad() {
        DispatchQueue.global(qos: .userInitiated).async {
            for _ in 0..<30 {
                if self.serverIsReachable() {
                    DispatchQueue.main.async {
                        self.webView?.load(URLRequest(url: self.dashboardURL))
                    }
                    return
                }
                Thread.sleep(forTimeInterval: 0.25)
            }
            DispatchQueue.main.async {
                self.showError("Nucleus server did not start. Check ~/Library/Logs/Nucleus/nucleus.log.")
            }
        }
    }

    private func serverIsReachable() -> Bool {
        let semaphore = DispatchSemaphore(value: 0)
        var ok = false
        let url = dashboardURL.appendingPathComponent("api/status")
        let task = URLSession.shared.dataTask(with: url) { _, response, _ in
            if let http = response as? HTTPURLResponse, http.statusCode == 200 {
                ok = true
            }
            semaphore.signal()
        }
        task.resume()
        _ = semaphore.wait(timeout: .now() + 1.0)
        return ok
    }

    private func showError(_ message: String) {
        let alert = NSAlert()
        alert.messageText = "Nucleus"
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.runModal()
    }

    func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String, initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping () -> Void) {
        let alert = NSAlert()
        alert.messageText = "Nucleus"
        alert.informativeText = message
        alert.addButton(withTitle: "OK")
        alert.runModal()
        completionHandler()
    }

    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String, initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (Bool) -> Void) {
        let alert = NSAlert()
        alert.messageText = "Nucleus"
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.addButton(withTitle: "Delete")
        alert.addButton(withTitle: "Cancel")
        completionHandler(alert.runModal() == .alertFirstButtonReturn)
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
