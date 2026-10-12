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
    /// Traffic statistics (read by the app through "vela:stats").
    private var trafficURL: URL { dataURL.appendingPathComponent("traffic.json") }
    private var recording = false

    override func startTunnel(options: [String: NSObject]?) async throws {
        // the app's language, remembered for starts by iOS (On Demand)
        if let lang = options?["lang"] as? String { UserDefaults.standard.set(lang, forKey: "lang") }
        var step = tr("подготовка")
        do {
            try await start(options: options, step: &step)
            note("ok: tunnel started")
            log.info("tunnel started")
        } catch {
            // The app can't see the extension's errors by itself: keep the
            // reason in a file (read through the App Group when it exists)
            // and show it to the user directly.
            let text = String(format: tr("Vela не подключилась (%@): %@"), step, error.localizedDescription)
            note("ERROR: " + text)
            log.error("\(text, privacy: .public)")
            // nothing half-started stays behind for the next start
            locked { stopService() }
            commandServer?.close()
            commandServer = nil
            displayMessage(text) { _ in }
            throw TunnelError(text)
        }
    }

    private func start(options: [String: NSObject]?, step: inout String) async throws {
        let base = dataURL
        step = tr("папки")
        let working = base.appendingPathComponent("core", isDirectory: true)
        try? FileManager.default.createDirectory(at: working, withIntermediateDirectories: true)
        try? Data().write(to: base.appendingPathComponent("start.log"))
        note("start, data: \(base.path), app group: \(hasAppGroup)")
        step = tr("правила маршрутизации")
        try installRules(into: base.appendingPathComponent("rules", isDirectory: true))

        // The app passes the config; when the system starts the tunnel by itself
        // (Connect On Demand, Settings) the last config saved here is used.
        step = tr("конфигурация")
        let snapshot = base.appendingPathComponent("tunnel-config.json")
        if let config = options?["configContent"] as? String {
            configContent = config
            try? config.write(to: snapshot, atomically: true, encoding: .utf8)
        } else {
            configContent = try? String(contentsOf: snapshot, encoding: .utf8)
        }
        guard let config = configContent else {
            throw TunnelError(tr("откройте Vela и подключитесь из приложения"))
        }
        note("config: \(config.utf8.count) bytes, memory: \(memoryMB()) MB")
        // a fresh log for every connection
        try? Data().write(to: base.appendingPathComponent("core.log"))

        step = tr("запуск ядра")
        let setup = LibboxSetupOptions()
        setup.basePath = base.path
        setup.workingPath = working.path
        setup.tempPath = FileManager.default.temporaryDirectory.path
        setup.logMaxLines = 1000
        setup.oomKillerEnabled = true
        // TCP instead of a unix socket: the socket path in the extension's own
        // container (re-signed builds without App Group) is longer than the
        // 104 characters iOS allows, and the core could not start at all.
        setup.commandServerListenPort = Int32.random(in: 10000 ... 19999)
        setup.commandServerSecret = UUID().uuidString
        var error: NSError?
        LibboxSetup(setup, &error)
        if let error { throw error }

        step = tr("служба управления")
        commandServer = LibboxNewCommandServer(platform, platform, &error)
        if let error { throw error }
        try commandServer!.start()
        step = tr("подключение к серверу")
        try startService()
        note("service started, memory: \(memoryMB()) MB")
    }

    private var hasAppGroup: Bool {
        let group = Bundle.main.object(forInfoDictionaryKey: "AppGroupIdentifier") as? String ?? ""
        return !group.isEmpty && FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group) != nil
    }

    /// Appends a line to start.log (shown in the app when the App Group works).
    private func note(_ line: String) {
        let url = dataURL.appendingPathComponent("start.log")
        let stamp = ISO8601DateFormatter().string(from: Date())
        let data = Data("\(stamp) \(line)\n".utf8)
        if let h = try? FileHandle(forWritingTo: url) {
            h.seekToEndOfFile()
            h.write(data)
            try? h.close()
        } else {
            try? data.write(to: url)
        }
    }

    private func memoryMB() -> Int {
        var info = task_vm_info_data_t()
        var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<natural_t>.size)
        let kr = withUnsafeMutablePointer(to: &info) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
            }
        }
        return kr == KERN_SUCCESS ? Int(info.phys_footprint / 1_048_576) : -1
    }

    /// Rule sets of the routing modes ship inside the extension bundle
    /// (core/rules): every .srs file there is copied, so new lists need no
    /// change here.
    private func installRules(into dir: URL) throws {
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        var sources = Bundle.main.urls(forResourcesWithExtension: "srs", subdirectory: nil) ?? []
        sources += Bundle.main.urls(forResourcesWithExtension: "srs", subdirectory: "rules") ?? []
        // an update may bring new lists of the same size: copy all once per build
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? ""
        let fresh = UserDefaults.standard.string(forKey: "rulesBuild") != build
        for source in sources {
            let target = dir.appendingPathComponent(source.lastPathComponent)
            let srcSize = (try? FileManager.default.attributesOfItem(atPath: source.path)[.size] as? Int) ?? -1
            let dstSize = (try? FileManager.default.attributesOfItem(atPath: target.path)[.size] as? Int) ?? -2
            if srcSize == dstSize, !fresh { continue }
            try? FileManager.default.removeItem(at: target)
            try FileManager.default.copyItem(at: source, to: target)
        }
        UserDefaults.standard.set(build, forKey: "rulesBuild")
    }

    func startService() throws {
        guard let raw = configContent, let commandServer else { return }
        let config = raw.replacingOccurrences(of: Self.dataPlaceholder, with: dataURL.path)
        // the running core's numbers are saved before it is replaced
        stopRecorder()
        do {
            try commandServer.startOrReloadService(config, options: LibboxOverrideOptions())
        } catch {
            throw TunnelError(String(format: tr("Не удалось подключиться: %@"), error.localizedDescription))
        }
        startRecorder(config)
    }

    func stopService() {
        stopRecorder()
        try? commandServer?.closeService()
        platform.reset()
    }

    /// Counts traffic through the core's local controller (port and secret
    /// come from the config the app built).
    private func startRecorder(_ config: String) {
        guard let api = Self.controller(in: config) else { return }
        VelaextStartTrafficRecorder(Int32(api.port), api.secret, trafficURL.path)
        recording = true
    }

    /// Saves the last numbers; the core must still be running.
    private func stopRecorder() {
        guard recording else { return }
        recording = false
        VelaextStopTrafficRecorder()
    }

    /// Port and secret of clash_api in the sing-box config, found without
    /// parsing the whole (possibly large) JSON.
    static func controller(in config: String) -> (port: Int, secret: String)? {
        // only the clash_api block is searched: a regex over the whole config
        // would copy it (UTF-16) in the 50 MB of the extension
        guard let start = config.range(of: "\"clash_api\"") else { return nil }
        let block = String(config[start.lowerBound...].prefix(600))
        func first(_ pattern: String) -> String? {
            guard let re = try? NSRegularExpression(pattern: pattern),
                  let m = re.firstMatch(in: block, range: NSRange(block.startIndex..., in: block)),
                  m.numberOfRanges > 1, let r = Range(m.range(at: 1), in: block) else { return nil }
            return String(block[r])
        }
        guard let port = first(#""external_controller"\s*:\s*"[^"]*:(\d+)""#).flatMap({ Int($0) }), port > 0 else { return nil }
        let secret = first(#""clash_api"\s*:\s*\{[^}]*"secret"\s*:\s*"([^"]*)""#) ?? ""
        return (port, secret)
    }

    override func stopTunnel(with reason: NEProviderStopReason) async {
        log.info("stopping: \(reason.rawValue)")
        // why iOS stopped the tunnel (1 user, 3 no network, 9/10 profile
        // disabled/removed, 11 another VPN, 16 app update…), for the report
        note("stop: reason \(reason.rawValue), memory: \(memoryMB()) MB")
        locked { stopService() }
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
        if config == "vela:memory" { return Data("\(memoryMB())".utf8) }
        if config == "vela:stats" { return Data(VelaextTrafficSummary(trafficURL.path).utf8) }
        guard config.first(where: { !$0.isWhitespace }) == "{" else { return nil } // unknown command
        trimLog()
        reasserting = true
        defer { reasserting = false }
        return locked { applyConfig(config) }
    }

    /// Messages can arrive while another reload or the stop is running.
    private let serviceLock = NSLock()

    private func locked<T>(_ body: () -> T) -> T {
        serviceLock.lock()
        defer { serviceLock.unlock() }
        return body()
    }

    /// Starts the core with a new config; on failure the previous one comes back.
    private func applyConfig(_ config: String) -> Data? {
        guard commandServer != nil else { return Data(tr("откройте Vela и подключитесь из приложения").utf8) }
        let previous = configContent
        configContent = config
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

/// Localized text of the extension, in the app's language when the app
/// passed it (the extension has its own settings, so it can't see the
/// app's per-app language).
func tr(_ key: String) -> String {
    let lang = UserDefaults.standard.string(forKey: "lang") ?? ""
    if !lang.isEmpty, let path = Bundle.main.path(forResource: lang, ofType: "lproj"), let bundle = Bundle(path: path) {
        return bundle.localizedString(forKey: key, value: key, table: nil)
    }
    return NSLocalizedString(key, comment: "")
}

struct TunnelError: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}
