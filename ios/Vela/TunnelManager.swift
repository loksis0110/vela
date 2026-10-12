import Foundation
import NetworkExtension
import Velacore
import WidgetKit

enum ConnState { case disconnected, connecting, connected, disconnecting }

struct TrafficPoint: Equatable {
    let up: Int64
    let down: Int64
}

/// What websites see (from MyIP in the Go core).
struct IPInfo: Equatable {
    let ip, country, city, isp, flag: String
}

struct SpeedState: Equatable {
    var running = false
    var phase = ""
    var pingMs = 0.0
    var downMbps = 0.0
    var upMbps = 0.0
    var error: String?
    var done = false
}

/// Bridges speed test progress from Go to Swift.
private final class SpeedCallback: NSObject, VelacoreSpeedHandlerProtocol {
    let handler: (String, Double) -> Void
    init(_ handler: @escaping (String, Double) -> Void) { self.handler = handler }
    func onProgress(_ phase: String?, value: Double) { handler(phase ?? "", value) }
}

/// Owns the VPN configuration in iOS Settings and talks to the running core.
@MainActor
final class TunnelManager: ObservableObject {
    static let shared = TunnelManager()

    @Published private(set) var state: ConnState = .disconnected
    @Published var error: String?
    @Published private(set) var since: Date?
    @Published private(set) var up: Int64 = 0
    @Published private(set) var down: Int64 = 0
    @Published private(set) var total: Int64 = 0
    /// Node id that currently carries traffic (resolves "auto").
    @Published private(set) var activeNode: String?
    /// Calm message, e.g. that Vela switched to another server.
    @Published var notice: String?
    /// Last two minutes of traffic per second.
    @Published private(set) var history: [TrafficPoint] = []
    @Published private(set) var ip: IPInfo?
    @Published private(set) var ipBusy = false
    @Published private(set) var ipError: String?
    @Published var speed = SpeedState()
    /// Open connections of the running core (from the stats poll).
    @Published private(set) var connectionCount = 0
    /// Traffic statistics: live from the extension while connected, the
    /// last saved copy otherwise.
    @Published private(set) var summary: TrafficSummary?
    @Published private(set) var summaryLive = false
    @Published private(set) var summaryDate: Date?

    private var watchTask: Task<Void, Never>?
    /// Set when this app instance started the connection (not iOS on its own
    /// or an earlier launch): only then a failed first check means the chosen
    /// server is down right now.
    private var startedHere = false
    nonisolated static let testURL = "https%3A%2F%2Fwww.gstatic.com%2Fgenerate_204"

    private var manager: NETunnelProviderManager?
    private var observer: NSObjectProtocol?
    private var statsTask: Task<Void, Never>?

    // The controller port and secret are stored so the app can reattach after relaunch.
    private var port: Int {
        get { UserDefaults.standard.integer(forKey: "controllerPort") }
        set { UserDefaults.standard.set(newValue, forKey: "controllerPort") }
    }
    private var secret: String {
        get { UserDefaults.standard.string(forKey: "controllerSecret") ?? "" }
        set { UserDefaults.standard.set(newValue, forKey: "controllerSecret") }
    }

    /// Controller of the running core, for the connections screen.
    var controller: (port: Int, secret: String)? {
        state == .connected && port != 0 ? (port, secret) : nil
    }

    /// Bundle id of the embedded Packet Tunnel extension. Read from the app
    /// bundle because re-signing tools may change bundle identifiers.
    static let tunnelBundleID: String = {
        if let plugins = Bundle.main.builtInPlugInsURL,
           let items = try? FileManager.default.contentsOfDirectory(at: plugins, includingPropertiesForKeys: nil) {
            for appex in items where appex.pathExtension == "appex" {
                if let bundle = Bundle(url: appex),
                   let point = (bundle.infoDictionary?["NSExtension"] as? [String: Any])?["NSExtensionPointIdentifier"] as? String,
                   point == "com.apple.networkextension.packet-tunnel",
                   let id = bundle.bundleIdentifier {
                    return id
                }
            }
        }
        return (Bundle.main.bundleIdentifier ?? "app.vela.client") + ".tunnel"
    }()

    func load() async {
        let managers = (try? await NETunnelProviderManager.loadAllFromPreferences()) ?? []
        manager = managers.first {
            ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == Self.tunnelBundleID
        } ?? managers.first
        if let manager { observe(manager) }
        apply(manager?.connection.status ?? .disconnected)
    }

    private func observe(_ m: NETunnelProviderManager) {
        if let observer { NotificationCenter.default.removeObserver(observer) }
        observer = NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange, object: m.connection, queue: .main) { [weak self] _ in
            Task { @MainActor in self?.apply(m.connection.status) }
        }
    }

    private func apply(_ status: NEVPNStatus) {
        defer { refreshWidgets(status) }
        switch status {
        case .connected:
            let wasConnected = state == .connected
            state = .connected
            since = manager?.connection.connectedDate ?? Date()
            startStats()
            if !wasConnected {
                startWatchdog(fresh: startedHere)
                startedHere = false
                Task {
                    await self.attach()
                    try? await Task.sleep(nanoseconds: 1_200_000_000)
                    await self.refreshIP()
                }
            }
        case .connecting, .reasserting:
            state = .connecting
        case .disconnecting:
            state = .disconnecting
        default:
            let was = state
            state = .disconnected
            since = nil
            if was != .disconnected { ip = nil; ipError = nil }
            if was != .disconnected, !userStopped {
                // the tunnel stopped by itself: tell the user why
                Task { await self.explainDisconnect(wasConnected: was == .connected) }
            }
            if was != .disconnected, userStopped {
                // stopped from Vela: now the flag for the widget can be
                // brought up to date (left alone while the tunnel ran)
                Task { await self.applyAlwaysOn() }
            }
            userStopped = false
            // reloadPending stays: iOS may start the tunnel again with the
            // last config (On Demand), the changes are sent then
            appliedConfig = nil
            up = 0; down = 0; total = 0; activeNode = nil
            history = []
            connectionCount = 0
            if was == .connected { summaryLive = false }
            statsTask?.cancel()
            watchTask?.cancel()
        }
    }

    /// Set when the user presses «Отключить», so that isn't reported as a failure.
    private var userStopped = false
    /// Why the last connection failed (for the diagnostics report).
    @Published private(set) var lastFailure: String?

    private func explainDisconnect(wasConnected: Bool) async {
        guard let m = manager else { return }
        let err: Error? = await withCheckedContinuation { c in
            m.connection.fetchLastDisconnectError { c.resume(returning: $0) }
        }
        var text = err.map { e -> String in
            let ns = e as NSError
            return "\(ns.localizedDescription) [\(ns.domain) \(ns.code)]"
        }
        // the extension's own words, when the App Group lets us read them
        if let log = try? String(contentsOf: Store.shared.dataURL.appendingPathComponent("start.log"), encoding: .utf8),
           let line = log.split(separator: "\n").last(where: { $0.contains("ERROR:") }) {
            text = String(line.components(separatedBy: "ERROR: ").last ?? String(line))
        }
        guard let text else { return }
        lastFailure = text
        // a working connection that dropped is not "couldn't connect"
        error = wasConnected ? LF("VPN отключился: %@", text) : LF("Не удалось подключиться: %@", text)
    }

    /// Everything useful for finding out why the VPN doesn't work.
    func diagnostics() async -> String {
        let store = Store.shared
        var lines: [String] = []
        let info = Bundle.main.infoDictionary ?? [:]
        lines.append("Vela \(info["CFBundleShortVersionString"] ?? "?") (\(info["CFBundleVersion"] ?? "?")), iOS \(ProcessInfo.processInfo.operatingSystemVersionString)")
        let group = Bundle.main.object(forInfoDictionaryKey: "AppGroupIdentifier") as? String ?? ""
        let hasGroup = !group.isEmpty && FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group) != nil
        lines.append("App Group: " + (hasGroup ? L("есть") : L("нет (подпись без группы)")))
        lines.append(LF("Расширение VPN: %@", Self.tunnelBundleID))
        let managers = (try? await NETunnelProviderManager.loadAllFromPreferences()) ?? []
        lines.append(LF("Профилей VPN в iOS: %@", "\(managers.count)"))
        if let m = manager {
            lines.append(LF("Профиль включён: %@, On Demand: %@, статус: %@", "\(m.isEnabled)", "\(m.isOnDemandEnabled)", "\(m.connection.status.rawValue)"))
            let err: Error? = await withCheckedContinuation { c in m.connection.fetchLastDisconnectError { c.resume(returning: $0) } }
            if let e = err as NSError? { lines.append(LF("Последняя ошибка iOS: %@", "\(e.localizedDescription) [\(e.domain) \(e.code)]")) }
        } else {
            lines.append(L("Профиль VPN ещё не создан"))
        }
        if let f = lastFailure { lines.append(LF("Последняя ошибка Vela: %@", f)) }
        if let p = store.state.active {
            let sel = store.state.selectedNode
            let name = p.nodes.first { $0.id == sel }?.name ?? L("Автовыбор")
            lines.append(LF("Подписка: %@, серверов: %@, выбран: %@", p.displayName, "\(p.nodes.count)", name))
            if let ms = store.pings[sel] {
                lines.append(LF("Последняя проверка выбранного: %@", ms > 0 ? "\(ms) " + L("мс") : (store.reasons[sel] ?? L("не работает"))))
            }
        }
        do {
            let cfg = try store.buildConfig(port: 1, secret: "x")
            lines.append(LF("Конфигурация: %@, серверов в ней: %@", Fmt.bytes(Int64(cfg.utf8.count)), "\(cfg.components(separatedBy: "\"tag\": \"node-").count - 1)"))
        } catch {
            lines.append(LF("Конфигурация не собирается: %@", error.localizedDescription))
        }
        lines.append(LF("Маршрутизация: %@", store.state.settings.routing))
        if state == .connected, let mem = await recordMemory() {
            lines.append(LF("Память расширения: %@ МБ из ~50, пик %@ МБ", "\(mem.now)", "\(mem.peak)"))
        } else if let saved = UserDefaults.standard.string(forKey: "extMemory")?.split(separator: " "), saved.count == 2 {
            lines.append(LF("Память расширения в прошлый раз: пик %@ МБ из ~50 (%@)", String(saved[0]), String(saved[1])))
        }
        // the extension's own log: read directly with the App Group, or asked for
        var log = (try? String(contentsOf: store.dataURL.appendingPathComponent("start.log"), encoding: .utf8)) ?? ""
        if !hasGroup, state == .connected { log = await sendMessage("vela:startlog") ?? "" }
        if !log.isEmpty {
            lines.append("")
            lines.append(L("Журнал запуска:"))
            lines.append(log)
        }
        return lines.joined(separator: "\n")
    }

    private var lastWidgetStatus: NEVPNStatus?

    /// Keeps the home screen widget and the Control Center control in sync.
    private func refreshWidgets(_ status: NEVPNStatus) {
        guard status != lastWidgetStatus else { return }
        lastWidgetStatus = status
        WidgetCenter.shared.reloadAllTimelines()
        if #available(iOS 18.0, *) { ControlCenter.shared.reloadAllControls() }
    }

    private func ensureManager() async throws -> NETunnelProviderManager {
        // a fresh copy from iOS: the one in memory may be stale (changed in
        // Settings, by the widget) or removed, and saving it would fail
        let all = (try? await NETunnelProviderManager.loadAllFromPreferences()) ?? []
        let ours = all.filter { ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == Self.tunnelBundleID }
        let m = ours.first ?? all.first ?? NETunnelProviderManager()
        // duplicates left by quick double taps in 1.4.0
        for extra in ours.dropFirst() { try? await extra.removeFromPreferences() }
        let proto = (m.protocolConfiguration as? NETunnelProviderProtocol) ?? NETunnelProviderProtocol()
        proto.providerBundleIdentifier = Self.tunnelBundleID
        proto.serverAddress = "Vela"
        proto.disconnectOnSleep = false
        // the widget and the Control Center control read it (no shared App Group)
        proto.providerConfiguration = ["alwaysOn": Store.shared.state.alwaysOn]
        m.protocolConfiguration = proto
        m.localizedDescription = "Vela"
        m.isEnabled = true
        // «Постоянный VPN»: iOS brings the tunnel back after network changes and
        // restarts. Switched on once the tunnel is up (attach): on in this save,
        // iOS could start the tunnel itself first, with the previous config and port.
        let connect = NEOnDemandRuleConnect()
        connect.interfaceTypeMatch = .any
        m.onDemandRules = [connect]
        m.isOnDemandEnabled = false
        // iOS asks the user to allow adding the VPN configuration here the first time.
        try await m.saveToPreferences()
        try await m.loadFromPreferences()
        manager = m
        observe(m)
        return m
    }

    /// A start in progress: a second tap must not create a second VPN profile.
    private var starting = false
    /// Settings changed while the tunnel was connecting: applied once it's up.
    private var reloadPending = false
    /// Fingerprint of the config the running core has, so the same one isn't sent again.
    private var appliedConfig: String?

    /// Stable across launches and the same in the extension (FNV-1a).
    nonisolated static func fingerprint(_ text: String) -> String {
        var h: UInt64 = 0xCBF2_9CE4_8422_2325
        for b in text.utf8 {
            h ^= UInt64(b)
            h = h &* 0x0000_0100_0000_01B3
        }
        return String(h, radix: 16)
    }

    /// The tunnel is up (started here, by iOS or the widget, or found running
    /// at launch): learn its controller (the stored port may be another one
    /// after a start by iOS), pass the language, switch On Demand on and send
    /// settings changed while it wasn't connected.
    private func attach() async {
        if let reply = await sendMessage("vela:hello:" + Lang.code) {
            let o = Store.json(reply)
            if let p = (o["port"] as? NSNumber)?.intValue, p > 0 {
                port = p
                secret = o["secret"] as? String ?? ""
            }
            appliedConfig = o["config"] as? String
        }
        await enableOnDemand()
        if reloadPending { await reload() }
    }

    /// «Постоянный VPN»: On Demand goes on once the tunnel is up.
    private func enableOnDemand() async {
        guard Store.shared.state.alwaysOn, let m = manager else { return }
        await serially {
            try? await m.loadFromPreferences()
            // not after a quick «Отключить» or when the tunnel is already gone:
            // iOS would bring it back
            guard self.state == .connected, !self.userStopped, Store.shared.state.alwaysOn, !m.isOnDemandEnabled else { return }
            m.isOnDemandEnabled = true
            await Self.save(m)
        }
    }

    /// Changes of the saved VPN profile, one at a time (each one loads it,
    /// changes it and saves it with awaits in between).
    private var profileWork: Task<Void, Never>?

    private func serially(_ op: @escaping @MainActor () async -> Void) async {
        let previous = profileWork
        let task = Task { @MainActor in
            await previous?.value
            await op()
        }
        profileWork = task
        await task.value
    }

    func connect() async {
        guard !starting else { return }
        starting = true
        defer { starting = false }
        error = nil
        notice = nil
        userStopped = false
        startedHere = true
        reloadPending = false
        do {
            // checked first: without servers nothing is saved or changed
            let newPort = Int.random(in: 20000 ... 60000)
            let newSecret = Store.randomID(16)
            let config = try Store.shared.buildConfig(port: newPort, secret: newSecret)
            // a change of the VPN profile still running (a quick Disconnect
            // and Connect) would make this save fail as stale
            _ = await profileWork?.value
            let m = try await ensureManager()
            // the extension keeps the config of the last start for On Demand,
            // so the stored port changes only together with it
            let oldPort = port, oldSecret = secret
            port = newPort
            secret = newSecret
            do {
                try m.connection.startVPNTunnel(options: ["configContent": config as NSString, "lang": Lang.code as NSString])
                appliedConfig = Self.fingerprint(config)
            } catch {
                port = oldPort
                secret = oldSecret
                throw error
            }
        } catch let e as NEVPNError where e.code == .configurationReadWriteFailed {
            startedHere = false
            error = L("Разрешите Vela добавить конфигурацию VPN")
        } catch {
            startedHere = false
            self.error = error.localizedDescription
        }
    }

    func disconnect() {
        userStopped = true
        guard let m = manager else { return }
        Task {
            await self.serially {
                // On Demand (set here, by the widget or in Settings) would connect
                // again right away; the copy in memory may be stale, so refresh it
                try? await m.loadFromPreferences()
                if m.isOnDemandEnabled {
                    m.isOnDemandEnabled = false
                    await Self.save(m)
                }
                m.connection.stopVPNTunnel()
            }
        }
    }

    /// Saves a VPN configuration; a stale copy is refreshed and saved again.
    private static func save(_ m: NETunnelProviderManager) async {
        do {
            try await m.saveToPreferences()
        } catch {
            let onDemand = m.isOnDemandEnabled
            try? await m.loadFromPreferences()
            m.isOnDemandEnabled = onDemand
            try? await m.saveToPreferences()
        }
    }

    /// Applies «Постоянный VPN» to the saved VPN configuration.
    func applyAlwaysOn() async {
        guard let m = manager else { return }
        await serially {
            try? await m.loadFromPreferences()
            let always = Store.shared.state.alwaysOn
            let want = always && self.state != .disconnected
            var changed = m.isOnDemandEnabled != want
            m.isOnDemandEnabled = want
            // the flag for the widget; the protocol is left alone while the tunnel
            // runs (iOS may restart it), connect() writes it then
            if self.state == .disconnected, let proto = m.protocolConfiguration as? NETunnelProviderProtocol,
               proto.providerConfiguration?["alwaysOn"] as? Bool != always {
                proto.providerConfiguration = ["alwaysOn": always]
                m.protocolConfiguration = proto
                changed = true
            }
            if changed { await Self.save(m) }
        }
    }

    /// Applies new settings or another profile without dropping the tunnel.
    func reload() async {
        guard state == .connected, let session = manager?.connection as? NETunnelProviderSession else {
            // sent once the tunnel is up (connecting now, or started again by
            // iOS with the last config); connect() builds a fresh one anyway
            reloadPending = true
            return
        }
        reloadPending = false
        do {
            let config = try Store.shared.buildConfig(port: port, secret: secret)
            // the same config again would only restart the core for nothing
            let fingerprint = Self.fingerprint(config)
            guard fingerprint != appliedConfig else { return }
            try session.sendProviderMessage(Data(config.utf8)) { [weak self] reply in
                guard let reply, let text = String(data: reply, encoding: .utf8), !text.isEmpty else { return }
                // the extension went back to the previous config
                Task { @MainActor in
                    self?.appliedConfig = nil
                    self?.error = text
                }
            }
            appliedConfig = fingerprint
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Controller

    /// One session for the controller: a new URLSession per request (every
    /// second for the stats) is never freed.
    private nonisolated static let controllerSession: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.connectionProxyDictionary = [:]
        return URLSession(configuration: config)
    }()

    private func request(_ method: String, _ path: String, body: [String: Any]? = nil, timeout: TimeInterval = 8) async -> [String: Any]? {
        guard port != 0, let url = URL(string: "http://127.0.0.1:\(port)\(path)") else { return nil }
        var req = URLRequest(url: url, timeoutInterval: timeout)
        req.httpMethod = method
        req.setValue("Bearer \(secret)", forHTTPHeaderField: "Authorization")
        if let body {
            req.httpBody = try? JSONSerialization.data(withJSONObject: body)
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        return await Self.fetch(req)
    }

    /// Off the main thread: /connections can be large with many connections.
    private nonisolated static func fetch(_ req: URLRequest) async -> [String: Any]? {
        guard let (data, resp) = try? await controllerSession.data(for: req),
              let http = resp as? HTTPURLResponse, http.statusCode < 300 else { return nil }
        if data.isEmpty { return [:] }
        return (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
    }

    /// Switches server instantly; returns false when a reload is needed.
    func switchTo(_ nodeID: String) async -> Bool {
        let target = nodeID == "auto" ? "auto" : VelacoreNodeTag(nodeID)
        return await request("PUT", "/proxies/proxy", body: ["name": target]) != nil
    }

    private func startStats() {
        statsTask?.cancel()
        statsTask = Task { [weak self] in
            var last: (Int64, Int64)?
            var tick = 0
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 1_000_000_000)
                guard let self, self.state == .connected else { continue }
                guard let c = await self.request("GET", "/connections") else { continue }
                let u = (c["uploadTotal"] as? NSNumber)?.int64Value ?? 0
                let d = (c["downloadTotal"] as? NSNumber)?.int64Value ?? 0
                if let last {
                    self.up = max(0, u - last.0); self.down = max(0, d - last.1)
                    self.history.append(TrafficPoint(up: self.up, down: self.down))
                    if self.history.count > 120 { self.history.removeFirst(self.history.count - 120) }
                }
                last = (u, d)
                self.total = u + d
                self.connectionCount = (c["connections"] as? [Any])?.count ?? 0
                if tick % 3 == 0 { self.activeNode = await self.resolveActive() }
                // the extension's memory, for the report (iOS stops it at ~50 MB)
                if tick % 30 == 10 { _ = await self.recordMemory() }
                // the extension saves the statistics every 30 seconds
                if tick % 60 == 5 { await self.loadSummary() }
                tick += 1
            }
        }
    }

    /// Checks the server in use right after connecting and then every 30
    /// seconds; when the chosen server stops answering, switches to
    /// automatic selection (unless turned off in Settings).
    private func startWatchdog(fresh: Bool) {
        watchTask?.cancel()
        watchTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 2_500_000_000)
            var fails = 0
            var first = fresh
            while !Task.isCancelled {
                guard let self, self.state == .connected else { return }
                let ok = await self.request("GET", "/proxies/proxy/delay?url=\(Self.testURL)&timeout=8000", timeout: 11) != nil
                if ok {
                    fails = 0
                } else {
                    fails += 1
                    if first || fails >= 2 {
                        fails = 0
                        await self.failover(justConnected: first)
                    }
                }
                first = false
                try? await Task.sleep(nanoseconds: 30_000_000_000)
            }
        }
    }

    private func failover(justConnected: Bool) async {
        let store = Store.shared
        let st = store.state
        guard let profile = st.active else { return }
        let manual = st.selectedNode != "auto" && profile.nodes.contains { $0.id == st.selectedNode }
        if st.settings.disableFailover || !manual {
            if manual, justConnected {
                error = L("Выбранный сервер не отвечает. Выберите другой сервер или «Автовыбор»")
            } else if !manual {
                _ = await request("GET", "/group/auto/delay?url=\(Self.testURL)&timeout=20000", timeout: 25)
            }
            return
        }
        let name = profile.nodes.first { $0.id == st.selectedNode }?.name ?? L("Сервер")
        // remembered as failed, so automatic selection skips it next time
        store.pings[st.selectedNode] = -1
        store.reasons[st.selectedNode] = L("перестал отвечать во время работы")
        store.savePings()
        if await switchTo("auto") {
            store.select(profile: profile.id, node: "auto")
            error = nil
            notice = LF("«%@» не отвечает. Vela переключилась на автовыбор", name)
        } else if justConnected {
            error = L("Выбранный сервер не отвечает. Выберите другой сервер")
        }
    }

    // MARK: - Tools

    /// Asks a website which IP and country it sees (through the VPN when on).
    func refreshIP() async {
        guard !ipBusy else { return }
        ipBusy = true
        ipError = nil
        defer { ipBusy = false }
        let result: Result<[String: Any], Error> = await Task.detached(priority: .userInitiated) {
            var err: NSError?
            let text = VelacoreMyIP(&err)
            if let err { return .failure(CoreError(message: err.localizedDescription)) }
            return .success(Store.json(text))
        }.value
        switch result {
        case let .success(o):
            ip = IPInfo(ip: o["ip"] as? String ?? "", country: o["country"] as? String ?? "", city: o["city"] as? String ?? "",
                        isp: o["isp"] as? String ?? "", flag: o["flag"] as? String ?? "")
        case let .failure(e):
            ip = nil
            ipError = e.localizedDescription
        }
    }

    /// Measures ping, download and upload through the current connection.
    func runSpeedTest() async {
        guard !speed.running else { return }
        speed = SpeedState(running: true, phase: "ping")
        let callback = SpeedCallback { phase, value in
            Task { @MainActor in
                let t = TunnelManager.shared
                switch phase {
                case "ping": t.speed.pingMs = value; t.speed.phase = "download"
                case "download": t.speed.downMbps = value; t.speed.phase = "download"
                case "upload": t.speed.upMbps = value; t.speed.phase = "upload"
                default: break
                }
            }
        }
        let json = await Task.detached(priority: .userInitiated) { VelacoreSpeedTest(8, callback) }.value
        let o = Store.json(json)
        let err = (o["error"] as? String).flatMap { $0.isEmpty ? nil : $0 }
        speed = SpeedState(running: false, phase: "done", pingMs: (o["pingMs"] as? NSNumber)?.doubleValue ?? 0,
                           downMbps: (o["downloadMbps"] as? NSNumber)?.doubleValue ?? 0,
                           upMbps: (o["uploadMbps"] as? NSNumber)?.doubleValue ?? 0, error: err, done: true)
    }

    /// Recent core log from the Network Extension.
    func logs() async -> String {
        guard state == .connected else { return L("Журнал доступен, пока VPN подключён.") }
        let raw = await sendMessage("vela:logs") ?? ""
        let text = VelacoreTidyLog(raw)
        return text.isEmpty ? L("Журнал пуст.") : text
    }

    /// The extension's memory in MB, now and the peak; the peak is kept with
    /// the routing mode for the report after the tunnel is gone.
    private func recordMemory() async -> (now: Int, peak: Int)? {
        guard let reply = await sendMessage("vela:memory") else { return nil }
        let parts = reply.split(separator: " ").compactMap { Int($0) }
        guard let now = parts.first, now >= 0 else { return nil }
        let peak = max(now, parts.count > 1 ? parts[1] : now)
        UserDefaults.standard.set("\(peak) \(Store.shared.state.settings.routing)", forKey: "extMemory")
        return (now, peak)
    }

    /// Sends a command to the Network Extension and returns its reply.
    private func sendMessage(_ command: String) async -> String? {
        guard state == .connected, let session = manager?.connection as? NETunnelProviderSession else { return nil }
        return await withCheckedContinuation { cont in
            do {
                try session.sendProviderMessage(Data(command.utf8)) { reply in
                    cont.resume(returning: reply.flatMap { String(data: $0, encoding: .utf8) })
                }
            } catch {
                cont.resume(returning: nil)
            }
        }
    }

    // MARK: - Statistics and connections

    /// Loads the traffic statistics: from the extension while connected
    /// (its file is in its own container), straight from the file when the
    /// App Group is shared, otherwise the last saved copy.
    func loadSummary() async {
        var text: String?
        if state == .connected { text = await sendMessage("vela:stats") }
        if text.flatMap({ TrafficSummary(json: $0) }) == nil {
            let shared = Store.shared.dataURL.appendingPathComponent("traffic.json")
            if FileManager.default.fileExists(atPath: shared.path) {
                let path = shared.path
                text = await Task.detached { VelacoreTrafficSummary(path) }.value
            }
        }
        if let text, let s = TrafficSummary(json: text) {
            UserDefaults.standard.set(text, forKey: "trafficSummary")
            UserDefaults.standard.set(Date().timeIntervalSince1970, forKey: "trafficSummaryAt")
            summary = s.rebased()
            summaryLive = true
            summaryDate = Date()
        } else if let saved = UserDefaults.standard.string(forKey: "trafficSummary"), let s = TrafficSummary(json: saved) {
            summary = s.rebased()
            summaryLive = false
            let at = UserDefaults.standard.double(forKey: "trafficSummaryAt")
            summaryDate = at > 0 ? Date(timeIntervalSince1970: at) : nil
        }
    }

    /// Open connections of the running core, newest first.
    func connections() async -> Result<[Conn], CoreError> {
        guard let c = controller else { return .failure(CoreError(message: L("VPN не подключён"))) }
        return await Task.detached(priority: .userInitiated) { () -> Result<[Conn], CoreError> in
            var err: NSError?
            let text = VelacoreListConnections(Int32(c.port), c.secret, &err)
            if let err { return .failure(CoreError(message: err.localizedDescription)) }
            return .success(Conn.list(text))
        }.value
    }

    /// Closes one connection, or all of them when id is "".
    func closeConnection(_ id: String) async {
        guard let c = controller else { return }
        _ = await Task.detached(priority: .userInitiated) { () -> Bool in
            var err: NSError?
            return VelacoreCloseConnection(Int32(c.port), c.secret, id, &err)
        }.value
    }

    private func resolveActive() async -> String? {
        guard var now = await request("GET", "/proxies/proxy")?["now"] as? String else { return nil }
        if now == "auto" {
            guard let auto = await request("GET", "/proxies/auto")?["now"] as? String else { return nil }
            now = auto
        }
        return now.hasPrefix("node-") ? String(now.dropFirst(5)) : now
    }

    /// Stops the server check in progress.
    func stopProbe() {
        Store.shared.stopRequested = true
        VelacoreStopProbe()
        // requests to the running core already on their way end now, not
        // after their timeout
        inCoreChecks?.cancel()
    }

    /// Checks through the running core in progress (pingAll).
    private var inCoreChecks: Task<Void, Never>?

    /// Checks one server: through the running core when it's in use, otherwise
    /// with a temporary core.
    func pingOne(profile profileID: String, node nodeID: String) async {
        let store = Store.shared
        guard let p = store.state.profiles.first(where: { $0.id == profileID }),
              let n = p.nodes.first(where: { $0.id == nodeID }) else { return }
        // "measuring" right away
        store.pings[n.id] = 0
        store.reasons[n.id] = nil
        var inCore = false
        if state == .connected, p.id == store.state.activeProfile {
            inCore = await request("GET", "/proxies/\(VelacoreNodeTag(n.id))", timeout: 3) != nil
        }
        if inCore {
            let r = await request("GET", "/proxies/\(VelacoreNodeTag(n.id))/delay?url=\(Self.testURL)&timeout=6000", timeout: 9)
            let ms = (r?["delay"] as? NSNumber)?.intValue ?? -1
            store.pings[n.id] = ms > 0 ? ms : -1
            if ms <= 0 { store.reasons[n.id] = L("сервер не ответил") }
            store.savePings()
        } else {
            await store.probeOne(n)
        }
    }

    /// Real check of every server of a profile. While disconnected the app
    /// runs a temporary core and shows why each failing server fails; while
    /// connected the running core is asked through its controller. Results
    /// appear as they arrive.
    func pingAll(_ profileID: String? = nil) async {
        let store = Store.shared
        guard let profile = store.state.profiles.first(where: { $0.id == (profileID ?? store.state.activeProfile) }) ?? store.state.active,
              store.probe == nil else { return }
        let connected = state != .disconnected
        if !connected {
            await store.probeOffline(profile)
            return
        }
        if profile.id != store.state.activeProfile {
            error = L("Чтобы проверить другую подписку, отключитесь")
            return
        }
        store.beginProbe(profile.nodes)
        // the running core holds only some of the servers (memory limit of
        // the extension); the rest are checked at the same time with a
        // temporary core in the app
        let all = await request("GET", "/proxies", timeout: 4)
        let present = Set((all?["proxies"] as? [String: Any] ?? [:]).keys)
        let inCore = profile.nodes.filter { present.contains(VelacoreNodeTag($0.id)) }
        let outside = profile.nodes.filter { !present.contains(VelacoreNodeTag($0.id)) }
        let noAnswer = L("сервер не ответил")
        await store.streaming { feed in
            async let rest: Void = store.probeWithTempCore(outside, feed: feed)
            let checks = Task { @MainActor in
                await withTaskGroup(of: (String, Int).self) { group in
                    var iterator = inCore.makeIterator()
                    func addNext() {
                        guard !store.stopRequested, let n = iterator.next() else { return }
                        group.addTask {
                            let path = "/proxies/\(VelacoreNodeTag(n.id))/delay?url=\(Self.testURL)&timeout=6000"
                            let r = await self.request("GET", path, timeout: 9)
                            return (n.id, (r?["delay"] as? NSNumber)?.intValue ?? -1)
                        }
                    }
                    for _ in 0 ..< 12 { addNext() }
                    for await (id, ms) in group {
                        feed.add(id, ms > 0 ? ms : -1, ms > 0 ? "" : noAnswer)
                        addNext()
                    }
                }
            }
            self.inCoreChecks = checks
            await checks.value
            self.inCoreChecks = nil
            await rest
        }
        store.endProbe(profile.nodes)
    }
}
