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
                Task { await self.explainDisconnect() }
            }
            userStopped = false
            up = 0; down = 0; total = 0; activeNode = nil
            history = []
            statsTask?.cancel()
            watchTask?.cancel()
        }
    }

    /// Set when the user presses «Отключить», so that isn't reported as a failure.
    private var userStopped = false
    /// Why the last connection failed (for the diagnostics report).
    @Published private(set) var lastFailure: String?

    private func explainDisconnect() async {
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
        error = "Не удалось подключиться: " + text
    }

    /// Everything useful for finding out why the VPN doesn't work.
    func diagnostics() async -> String {
        let store = Store.shared
        var lines: [String] = []
        let info = Bundle.main.infoDictionary ?? [:]
        lines.append("Vela \(info["CFBundleShortVersionString"] ?? "?") (\(info["CFBundleVersion"] ?? "?")), iOS \(ProcessInfo.processInfo.operatingSystemVersionString)")
        let group = Bundle.main.object(forInfoDictionaryKey: "AppGroupIdentifier") as? String ?? ""
        let hasGroup = !group.isEmpty && FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group) != nil
        lines.append("App Group: \(hasGroup ? "есть" : "нет (подпись без группы, журнал запуска недоступен)")")
        lines.append("Расширение VPN: \(Self.tunnelBundleID)")
        let managers = (try? await NETunnelProviderManager.loadAllFromPreferences()) ?? []
        lines.append("Профилей VPN в iOS: \(managers.count)")
        if let m = manager {
            lines.append("Профиль включён: \(m.isEnabled), On Demand: \(m.isOnDemandEnabled), статус: \(m.connection.status.rawValue)")
            let err: Error? = await withCheckedContinuation { c in m.connection.fetchLastDisconnectError { c.resume(returning: $0) } }
            if let e = err as NSError? { lines.append("Последняя ошибка iOS: \(e.localizedDescription) [\(e.domain) \(e.code)]") }
        } else {
            lines.append("Профиль VPN ещё не создан")
        }
        if let f = lastFailure { lines.append("Последняя ошибка Vela: \(f)") }
        if let p = store.state.active {
            let sel = store.state.selectedNode
            let name = p.nodes.first { $0.id == sel }?.name ?? "Автовыбор"
            lines.append("Подписка: \(p.name), серверов: \(p.nodes.count), выбран: \(name)")
            if let ms = store.pings[sel] { lines.append("Последняя проверка выбранного: \(ms > 0 ? "\(ms) мс" : (store.reasons[sel] ?? "не работает"))") }
        }
        do {
            let cfg = try store.buildConfig(port: 1, secret: "x")
            lines.append("Конфигурация: \(cfg.utf8.count / 1024) КБ, серверов в ней: \(cfg.components(separatedBy: "\"tag\": \"node-").count - 1)")
        } catch {
            lines.append("Конфигурация не собирается: \(error.localizedDescription)")
        }
        if state == .connected, let session = manager?.connection as? NETunnelProviderSession {
            let mem: String = await withCheckedContinuation { c in
                do {
                    try session.sendProviderMessage(Data("vela:memory".utf8)) { c.resume(returning: $0.flatMap { String(data: $0, encoding: .utf8) } ?? "?") }
                } catch { c.resume(returning: "?") }
            }
            lines.append("Память расширения: \(mem) МБ из ~50")
        }
        if let log = try? String(contentsOf: store.dataURL.appendingPathComponent("start.log"), encoding: .utf8), !log.isEmpty {
            lines.append("")
            lines.append("Журнал запуска:")
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
        let m = manager ?? NETunnelProviderManager()
        let proto = (m.protocolConfiguration as? NETunnelProviderProtocol) ?? NETunnelProviderProtocol()
        proto.providerBundleIdentifier = Self.tunnelBundleID
        proto.serverAddress = "Vela"
        proto.disconnectOnSleep = false
        m.protocolConfiguration = proto
        m.localizedDescription = "Vela"
        m.isEnabled = true
        // «Постоянный VPN»: iOS brings the tunnel back after network changes and restarts
        let connect = NEOnDemandRuleConnect()
        connect.interfaceTypeMatch = .any
        m.onDemandRules = [connect]
        m.isOnDemandEnabled = Store.shared.state.alwaysOn
        // iOS asks the user to allow adding the VPN configuration here the first time.
        try await m.saveToPreferences()
        try await m.loadFromPreferences()
        manager = m
        observe(m)
        return m
    }

    func connect() async {
        error = nil
        notice = nil
        userStopped = false
        startedHere = true
        do {
            let m = try await ensureManager()
            port = Int.random(in: 20000 ... 60000)
            secret = Store.randomID(16)
            let config = try Store.shared.buildConfig(port: port, secret: secret)
            try m.connection.startVPNTunnel(options: ["configContent": config as NSString])
        } catch let e as NEVPNError where e.code == .configurationReadWriteFailed {
            error = "Разрешите Vela добавить конфигурацию VPN"
        } catch {
            self.error = error.localizedDescription
        }
    }

    func disconnect() {
        userStopped = true
        guard let m = manager else { return }
        if m.isOnDemandEnabled {
            // otherwise iOS would connect again right away
            m.isOnDemandEnabled = false
            Task {
                try? await m.saveToPreferences()
                m.connection.stopVPNTunnel()
            }
        } else {
            m.connection.stopVPNTunnel()
        }
    }

    /// Applies «Постоянный VPN» to the saved VPN configuration.
    func applyAlwaysOn() async {
        guard let m = manager else { return }
        let want = Store.shared.state.alwaysOn && state != .disconnected
        guard m.isOnDemandEnabled != want else { return }
        m.isOnDemandEnabled = want
        try? await m.saveToPreferences()
    }

    /// Applies new settings or another profile without dropping the tunnel.
    func reload() async {
        guard state == .connected, let session = manager?.connection as? NETunnelProviderSession else { return }
        do {
            let config = try Store.shared.buildConfig(port: port, secret: secret)
            try session.sendProviderMessage(Data(config.utf8)) { [weak self] reply in
                guard let reply, let text = String(data: reply, encoding: .utf8) else { return }
                Task { @MainActor in self?.error = text }
            }
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Controller

    private func request(_ method: String, _ path: String, body: [String: Any]? = nil, timeout: TimeInterval = 8) async -> [String: Any]? {
        guard port != 0, let url = URL(string: "http://127.0.0.1:\(port)\(path)") else { return nil }
        var req = URLRequest(url: url, timeoutInterval: timeout)
        req.httpMethod = method
        req.setValue("Bearer \(secret)", forHTTPHeaderField: "Authorization")
        if let body {
            req.httpBody = try? JSONSerialization.data(withJSONObject: body)
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let config = URLSessionConfiguration.ephemeral
        config.connectionProxyDictionary = [:]
        guard let (data, resp) = try? await URLSession(configuration: config).data(for: req),
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
                if tick % 3 == 0 { self.activeNode = await self.resolveActive() }
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
                error = "Выбранный сервер не отвечает. Выберите другой сервер или «Автовыбор»"
            } else if !manual {
                _ = await request("GET", "/group/auto/delay?url=\(Self.testURL)&timeout=20000", timeout: 25)
            }
            return
        }
        let name = profile.nodes.first { $0.id == st.selectedNode }?.name ?? "Сервер"
        // remembered as failed, so automatic selection skips it next time
        store.pings[st.selectedNode] = -1
        store.reasons[st.selectedNode] = "перестал отвечать во время работы"
        store.savePings()
        if await switchTo("auto") {
            store.select(profile: profile.id, node: "auto")
            error = nil
            notice = "«\(name)» не отвечает. Vela переключилась на автовыбор"
        } else if justConnected {
            error = "Выбранный сервер не отвечает. Выберите другой сервер"
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
        guard state == .connected, let session = manager?.connection as? NETunnelProviderSession else {
            return "Журнал доступен, пока VPN подключён."
        }
        let raw: String = await withCheckedContinuation { cont in
            do {
                try session.sendProviderMessage(Data("vela:logs".utf8)) { reply in
                    cont.resume(returning: reply.flatMap { String(data: $0, encoding: .utf8) } ?? "")
                }
            } catch {
                cont.resume(returning: "")
            }
        }
        let text = VelacoreTidyLog(raw)
        return text.isEmpty ? "Журнал пуст." : text
    }

    private func resolveActive() async -> String? {
        guard var now = await request("GET", "/proxies/proxy")?["now"] as? String else { return nil }
        if now == "auto" {
            guard let auto = await request("GET", "/proxies/auto")?["now"] as? String else { return nil }
            now = auto
        }
        return now.hasPrefix("node-") ? String(now.dropFirst(5)) : now
    }

    /// Real check of every server of a profile. While disconnected the app
    /// runs a temporary core and shows why each failing server fails; while
    /// connected the running core is asked through its controller.
    /// Stops the server check in progress.
    func stopProbe() {
        Store.shared.stopRequested = true
        VelacoreStopProbe()
    }

    /// Checks one server: through the running core when it's in use, otherwise
    /// with a temporary core.
    func pingOne(profile profileID: String, node nodeID: String) async {
        let store = Store.shared
        guard let p = store.state.profiles.first(where: { $0.id == profileID }),
              let n = p.nodes.first(where: { $0.id == nodeID }) else { return }
        var inCore = false
        if state == .connected, p.id == store.state.activeProfile {
            inCore = await request("GET", "/proxies/\(VelacoreNodeTag(n.id))") != nil
        }
        if inCore {
            store.pings[n.id] = 0
            store.reasons[n.id] = nil
            let r = await request("GET", "/proxies/\(VelacoreNodeTag(n.id))/delay?url=\(Self.testURL)&timeout=8000", timeout: 11)
            let ms = (r?["delay"] as? NSNumber)?.intValue ?? -1
            store.pings[n.id] = ms > 0 ? ms : -1
            if ms <= 0 { store.reasons[n.id] = "сервер не ответил" }
            store.savePings()
        } else {
            await store.probeOne(n)
        }
    }

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
            error = "Чтобы проверить другую подписку, отключитесь"
            return
        }
        for n in profile.nodes { store.pings[n.id] = 0; store.reasons[n.id] = nil }
        store.probe = (0, profile.nodes.count)
        store.stopRequested = false
        // the running core holds only some of the servers (memory limit of
        // the extension); the rest are checked with a temporary core
        let all = await request("GET", "/proxies")
        let present = Set((all?["proxies"] as? [String: Any] ?? [:]).keys)
        let inCore = profile.nodes.filter { present.contains(VelacoreNodeTag($0.id)) }
        let outside = profile.nodes.filter { !present.contains(VelacoreNodeTag($0.id)) }
        await withTaskGroup(of: (String, Int).self) { group in
            var started = 0
            var iterator = inCore.makeIterator()
            func addNext() {
                guard !store.stopRequested, let n = iterator.next() else { return }
                started += 1
                group.addTask {
                    let path = "/proxies/\(VelacoreNodeTag(n.id))/delay?url=\(Self.testURL)&timeout=8000"
                    let r = await self.request("GET", path, timeout: 11)
                    return (n.id, (r?["delay"] as? NSNumber)?.intValue ?? -1)
                }
            }
            for _ in 0 ..< 12 { addNext() }
            for await (id, ms) in group {
                if ms <= 0, store.stopRequested { store.pings[id] = nil; continue }
                store.pings[id] = ms > 0 ? ms : -1
                if ms <= 0 { store.reasons[id] = "сервер не ответил" }
                if let p = store.probe { store.probe = (p.done + 1, p.total) }
                addNext()
            }
        }
        if !store.stopRequested { await store.probeWithTempCore(outside) }
        for n in profile.nodes where store.pings[n.id] == 0 { store.pings[n.id] = nil }
        store.probe = nil
        store.savePings()
    }
}
