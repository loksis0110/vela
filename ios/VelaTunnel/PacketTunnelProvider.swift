import Foundation
import Libbox
import NetworkExtension
import os

/// Runs the sing-box core inside the Network Extension.
/// Adapted from sing-box-for-apple (GPL-3.0).
final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let log = Logger(subsystem: "app.vela.client.tunnel", category: "tunnel")
    private(set) var commandServer: LibboxCommandServer?
    private lazy var platform = PlatformInterface(self)
    private var configContent: String?

    /// Shared with the app when the App Group is available (App Store / own
    /// developer signing). Re-signed builds often lose the App Group, so the
    /// extension falls back to its own container and gets everything it
    /// needs from the start options.
    private lazy var dataURL: URL = {
        let group = Bundle.main.object(forInfoDictionaryKey: "AppGroupIdentifier") as? String ?? ""
        if !group.isEmpty, let shared = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group) {
            return shared
        }
        let own = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Vela", isDirectory: true)
        try? FileManager.default.createDirectory(at: own, withIntermediateDirectories: true)
        return own
    }()

    private static let dataPlaceholder = "__VELA_DATA__"

    override func startTunnel(options: [String: NSObject]?) async throws {
        let base = dataURL
        let working = base.appendingPathComponent("core", isDirectory: true)
        try? FileManager.default.createDirectory(at: working, withIntermediateDirectories: true)
        try installRules(into: base.appendingPathComponent("rules", isDirectory: true))

        // The app passes the config; when the system starts the tunnel by itself
        // (Connect On Demand, Settings) the last config saved here is used.
        let snapshot = base.appendingPathComponent("tunnel-config.json")
        if let config = options?["configContent"] as? String {
            configContent = config
            try? config.write(to: snapshot, atomically: true, encoding: .utf8)
        } else {
            configContent = try? String(contentsOf: snapshot, encoding: .utf8)
        }
        guard configContent != nil else {
            throw TunnelError("Откройте Vela и подключитесь из приложения")
        }
        // a fresh log for every connection
        try? Data().write(to: base.appendingPathComponent("core.log"))

        let setup = LibboxSetupOptions()
        setup.basePath = base.path
        setup.workingPath = working.path
        setup.tempPath = FileManager.default.temporaryDirectory.path
        setup.logMaxLines = 1000
        setup.oomKillerEnabled = true
        var error: NSError?
        LibboxSetup(setup, &error)
        if let error { throw TunnelError("setup: \(error.localizedDescription)") }

        commandServer = LibboxNewCommandServer(platform, platform, &error)
        if let error { throw TunnelError("command server: \(error.localizedDescription)") }
        try commandServer!.start()
        try startService()
        log.info("tunnel started")
    }

    /// Rule sets for the "smart" mode ship inside the extension bundle.
    private func installRules(into dir: URL) throws {
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let names = ["geoip-ru.srs", "geosite-category-ru.srs", "geosite-category-ads-all.srs"]
        for name in names {
            let target = dir.appendingPathComponent(name)
            guard let source = Bundle.main.url(forResource: name, withExtension: nil) else { continue }
            let srcSize = (try? FileManager.default.attributesOfItem(atPath: source.path)[.size] as? Int) ?? -1
            let dstSize = (try? FileManager.default.attributesOfItem(atPath: target.path)[.size] as? Int) ?? -2
            if srcSize == dstSize { continue }
            try? FileManager.default.removeItem(at: target)
            try FileManager.default.copyItem(at: source, to: target)
        }
    }

    func startService() throws {
        guard let raw = configContent, let commandServer else { return }
        let config = raw.replacingOccurrences(of: Self.dataPlaceholder, with: dataURL.path)
        do {
            try commandServer.startOrReloadService(config, options: LibboxOverrideOptions())
        } catch {
            throw TunnelError("Не удалось подключиться: \(error.localizedDescription)")
        }
    }

    func stopService() {
        try? commandServer?.closeService()
        platform.reset()
    }

    override func stopTunnel(with reason: NEProviderStopReason) async {
        log.info("stopping: \(reason.rawValue)")
        stopService()
        if let server = commandServer {
            try? await Task.sleep(nanoseconds: 100 * NSEC_PER_MSEC)
            server.close()
            commandServer = nil
        }
    }

    /// The app sends a new config here to reload without dropping the tunnel.
    override func handleAppMessage(_ messageData: Data) async -> Data? {
        guard let config = String(data: messageData, encoding: .utf8), !config.isEmpty else { return nil }
        if config == "vela:logs" {
            trimLog()
            return tailLog()
        }
        trimLog()
        let previous = configContent
        configContent = config
        reasserting = true
        defer { reasserting = false }
        do {
            try startService()
            try? config.write(to: dataURL.appendingPathComponent("tunnel-config.json"), atomically: true, encoding: .utf8)
            return nil
        } catch {
            // the new config failed: bring the last working one back, so the
            // tunnel doesn't stay "on" with nothing behind it
            configContent = previous
            try? startService()
            return error.localizedDescription.data(using: .utf8)
        }
    }

    /// Keeps the log of a long session from growing without limit.
    private func trimLog() {
        let url = dataURL.appendingPathComponent("core.log")
        let size = (try? FileManager.default.attributesOfItem(atPath: url.path)[.size] as? Int) ?? 0
        if size > 2 * 1024 * 1024 { try? Data().write(to: url) }
    }

    /// Last lines of the core log for the app's log viewer.
    private func tailLog() -> Data {
        let url = dataURL.appendingPathComponent("core.log")
        guard let handle = try? FileHandle(forReadingFrom: url) else { return Data() }
        defer { try? handle.close() }
        let size = (try? handle.seekToEnd()) ?? 0
        let start = size > 256 * 1024 ? size - 256 * 1024 : 0
        try? handle.seek(toOffset: start)
        let data = (try? handle.readToEnd()) ?? Data()
        let text = String(decoding: data, as: UTF8.self)
        let lines = text.split(separator: "\n", omittingEmptySubsequences: true).suffix(300)
        return Data(lines.joined(separator: "\n").utf8)
    }

    override func sleep() async {
        commandServer?.pause()
    }

    override func wake() {
        commandServer?.wake()
    }
}

struct TunnelError: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}
