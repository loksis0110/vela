import Foundation
import Network
import NetworkExtension
import Velacore
import WidgetKit

enum ConnState { case disconnected, connecting, connected, disconnecting }

/// Whether traffic really flows through the connected tunnel (the core's
/// own request through the server in use).
enum LinkState { case checking, ok, down, offline }

struct TrafficPoint: Equatable {
    let up: Int64
    let down: Int64
}

/// Numbers that change every second while connected. Only the views that
/// show them observe this, so the rest of the app (the server list with
/// hundreds of rows) isn't redrawn every second. Changed on the main thread.
final class LiveStats: ObservableObject {
    @Published fileprivate(set) var up: Int64 = 0
    @Published fileprivate(set) var down: Int64 = 0
    @Published fileprivate(set) var total: Int64 = 0
    /// Last two minutes of traffic per second.
    @Published fileprivate(set) var history: [TrafficPoint] = []
    /// Open connections of the running core.
    @Published fileprivate(set) var connectionCount = 0

    fileprivate func reset() {
        if up != 0 { up = 0 }
        if down != 0 { down = 0 }
        if total != 0 { total = 0 }
        if !history.isEmpty { history = [] }
        if connectionCount != 0 { connectionCount = 0 }
    }
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
    /// Checked right after connecting: «Защищено» only once a request
    /// through the server went through.
    @Published private(set) var link: LinkState = .checking
    /// Traffic numbers of the running core (polled every second while the
    /// app is on screen).
    let live = LiveStats()
    /// Node id that currently carries traffic (resolves "auto").
    @Published private(set) var activeNode: String?
    /// Calm message, e.g. that Vela switched to another server.
    @Published var notice: String?
    @Published private(set) var ip: IPInfo?
    @Published private(set) var ipBusy = false
    @Published private(set) var ipError: String?
    @Published var speed = SpeedState()
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

    /// A load from iOS in progress: launch and becoming active ask at the same
    /// moment, one trip to the VPN settings serves both.
    private var loading: Task<Void, Never>?

    func load() async {
        if let loading { return await loading.value }
        let task = Task { @MainActor in
            let managers = (try? await NETunnelProviderManager.loadAllFromPreferences()) ?? []
            let m = managers.first {
                ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == Self.tunnelBundleID
            } ?? managers.first
            self.manager = m
            if let m { self.observe(m) }
            self.apply(m?.connection.status ?? .disconnected)
        }
        loading = task
        await task.value
        loading = nil
    }

    /// The app is on screen: the per-second numbers are polled only then.
    private var foreground = true
    private var lastCheck = Date.distantPast

    private init() {
        watchNetwork()
    }

    /// Wi-Fi ↔ mobile data and similar: the core moves its connections by
    /// itself; the app checks the server again a moment later (not every
    /// 30 s), once for a burst of changes.
    private let pathMonitor = NWPathMonitor()
    private var pathKey = ""
    private var recheck: Task<Void, Never>?

    private func watchNetwork() {
        pathMonitor.pathUpdateHandler = { @Sendable path in
            let key = "\(path.status) " + path.availableInterfaces.map { $0.name }.joined(separator: ",")
            Task { @MainActor in TunnelManager.shared.networkChanged(key) }
        }
        pathMonitor.start(queue: DispatchQueue(label: "app.vela.path"))
    }

    private func networkChanged(_ key: String) {
        let previous = pathKey
        pathKey = key
        guard !previous.isEmpty, key != previous, state == .connected, foreground else { return }
        recheck?.cancel()
        recheck = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 2_000_000_000)
            guard let self, !Task.isCancelled, self.state == .connected, self.link != .checking else { return }
            self.startWatchdog(fresh: false, verify: false)
        }
    }

    /// Called on scene phase changes: nothing is polled in the background,
    /// and coming back after a while checks the connection right away
    /// (the network may have changed meanwhile).
    func setForeground(_ on: Bool) {
        guard on != foreground else { return }
        foreground = on
        if !on {
            statsTask?.cancel()
            statsTask = nil
            return
        }
        guard state == .connected else { return }
        startStats()
        if Date().timeIntervalSince(lastCheck) > 20, link != .checking {
            startWatchdog(fresh: false, verify: false)
        }
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
            if !wasConnected {
                startStats()
                let fresh = startedHere
                startedHere = false
                // «Проверяем соединение…» until a request went through
                link = .checking
                watchTask?.cancel()
                Task {
                    if fresh {
                        // started here: port and secret are known, check at once
                        self.startWatchdog(fresh: true, verify: true)
                        await self.attach()
                    } else {
                        // started by iOS (On Demand, widget) or found running:
                        // the stored controller port may be an older one
                        await self.attach()
                        if self.state == .connected { self.startWatchdog(fresh: false, verify: true) }
                    }
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
            live.reset()
            if activeNode != nil { activeNode = nil }
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
            let cfg = try await store.buildConfig(port: 1, secret: "x")
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

    /// The VPN profile to start: (profile, whether it was saved just now).
    private func ensureManager(force: Bool = false) async throws -> (NETunnelProviderManager, Bool) {
        // a fresh copy from iOS: the one in memory may be stale (changed in
        // Settings, by the widget) or removed, and saving it would fail
        let all = (try? await NETunnelProviderManager.loadAllFromPreferences()) ?? []
        let ours = all.filter { ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == Self.tunnelBundleID }
        let m = ours.first ?? all.first ?? NETunnelProviderManager()
        // duplicates left by quick double taps in 1.4.0
        for extra in ours.dropFirst() { try? await extra.removeFromPreferences() }
        // Saving the profile is the slowest step of connecting (and makes iOS
        // reload its VPN settings): only when something in it has to change.
        let alwaysOn = Store.shared.state.alwaysOn
        if !force, let current = m.protocolConfiguration as? NETunnelProviderProtocol,
           current.providerBundleIdentifier == Self.tunnelBundleID, current.serverAddress == "Vela",
           !current.disconnectOnSleep, current.providerConfiguration?["alwaysOn"] as? Bool == alwaysOn,
           m.localizedDescription == "Vela", m.isEnabled, !m.isOnDemandEnabled,
           let rules = m.onDemandRules, rules.count == 1,
           (rules[0] as? NEOnDemandRuleConnect)?.interfaceTypeMatch == .any {
            manager = m
            observe(m)
            return (m, false)
        }
        let proto = (m.protocolConfiguration as? NETunnelProviderProtocol) ?? NETunnelProviderProtocol()
        proto.providerBundleIdentifier = Self.tunnelBundleID
        proto.serverAddress = "Vela"
        proto.disconnectOnSleep = false
        // the widget and the Control Center control read it (no shared App Group)
        proto.providerConfiguration = ["alwaysOn": alwaysOn]
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
        return (m, true)
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
            let request = try Store.shared.configRequest(port: newPort, secret: newSecret)
            // the core builds the config while iOS loads the VPN profile
            async let built = Store.build(request)
            // a change of the VPN profile still running (a quick Disconnect
            // and Connect) would make this save fail as stale
            _ = await profileWork?.value
            let (m, saved) = try await ensureManager()
            let config = try await built
            // the extension keeps the config of the last start for On Demand,
            // so the stored port changes only together with it
            let oldPort = port, oldSecret = secret
            port = newPort
            secret = newSecret
            let options: [String: NSObject] = ["configContent": config as NSString, "lang": Lang.code as NSString]
            do {
                do {
                    try m.connection.startVPNTunnel(options: options)
                } catch {
                    guard !saved else { throw error }
                    // the profile wasn't saved (nothing to change): an iOS
                    // copy gone stale meanwhile is saved again, then started
                    let (fresh, _) = try await ensureManager(force: true)
                    try fresh.connection.startVPNTunnel(options: options)
                }
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
    /// Only the newest of reloads started one after another is sent (the
    /// config is built off the main thread, an older one may finish later).
    private var reloadSeq = 0

    func reload() async {
        guard state == .connected, let session = manager?.connection as? NETunnelProviderSession else {
            // sent once the tunnel is up (connecting now, or started again by
            // iOS with the last config); connect() builds a fresh one anyway
            reloadPending = true
            return
        }
        reloadPending = false
        reloadSeq += 1
        let seq = reloadSeq
        do {
            let config = try await Store.build(Store.shared.configRequest(port: port, secret: secret))
            guard seq == reloadSeq else { return }
            guard state == .connected else {
                reloadPending = true
                return
            }
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
    /// The new server is checked right away (verify), like a new connection.
    func switchTo(_ nodeID: String, verify: Bool = true) async -> Bool {
        let target = nodeID == "auto" ? "auto" : VelacoreNodeTag(nodeID)
        let ok = await request("PUT", "/proxies/proxy", body: ["name": target]) != nil
        if ok, verify, state == .connected { startWatchdog(fresh: true, verify: true) }
        return ok
    }

    private func startStats() {
        statsTask?.cancel()
        statsTask = nil
        // nothing is polled while the app isn't on screen
        guard foreground else { return }
        statsTask = Task { [weak self] in
            var last: (Int64, Int64)?
            var tick = 0
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 1_000_000_000)
                guard let self, !Task.isCancelled else { return }
                guard self.state == .connected else { continue }
                guard let c = await self.request("GET", "/connections"), !Task.isCancelled else { continue }
                let u = (c["uploadTotal"] as? NSNumber)?.int64Value ?? 0
                let d = (c["downloadTotal"] as? NSNumber)?.int64Value ?? 0
                let live = self.live
                if let last {
                    let up = max(0, u - last.0), down = max(0, d - last.1)
                    if live.up != up { live.up = up }
                    if live.down != down { live.down = down }
                    var h = live.history
                    h.append(TrafficPoint(up: up, down: down))
                    if h.count > 120 { h.removeFirst(h.count - 120) }
                    // an idle line stays an idle line: no redraw for it
                    if h != live.history { live.history = h }
                }
                last = (u, d)
                if live.total != u + d { live.total = u + d }
                let count = (c["connections"] as? [Any])?.count ?? 0
                if live.connectionCount != count { live.connectionCount = count }
                if tick % 3 == 0 {
                    let now = await self.resolveActive()
                    if now != self.activeNode { self.activeNode = now }
                }
                // the extension's memory, for the report (iOS stops it at ~50 MB)
                if tick % 30 == 10 { _ = await self.recordMemory() }
                // the extension saves the statistics every 30 seconds
                if tick % 60 == 5 { await self.loadSummary() }
                tick += 1
            }
        }
    }

    /// Automatic selection is in use (no server chosen, or it's gone).
    private var autoSelected: Bool {
        let st = Store.shared.state
        guard let p = st.active else { return true }
        return st.selectedNode == "auto" || !p.nodes.contains { $0.id == st.selectedNode }
    }

    private func setLink(_ new: LinkState) {
        guard link != new else { return }
        let was = link
        link = new
        // a new connection or another server: the address websites see now
        if new == .ok, was == .checking || ip == nil { Task { await self.refreshIP() } }
    }

    /// Checks the server in use right away (verify: «Проверяем соединение…»
    /// until it answers) and then every 30 seconds. A server that stops
    /// answering is replaced within seconds: automatic selection moves on to
    /// a candidate that answers, a chosen server is switched to automatic
    /// selection (unless turned off in Settings). When the network itself is
    /// down nothing is blamed on the server.
    private func startWatchdog(fresh: Bool, verify: Bool) {
        watchTask?.cancel()
        if verify { link = .checking }
        watchTask = Task { [weak self] in
            var fails = 0
            var first = true
            while !Task.isCancelled {
                guard let self, self.state == .connected else { return }
                let auto = self.autoSelected
                // the first check decides what the screen says: a working
                // server answers in well under a second, a dead one is left quickly
                let limit = first ? (auto ? 3000 : 6000) : 8000
                let ms = await self.delay("proxy", timeoutMs: limit)
                guard !Task.isCancelled, self.state == .connected else { return }
                self.lastCheck = Date()
                var pause = 30.0
                if let ms, ms > 0 {
                    fails = 0
                    self.setLink(.ok)
                } else if ms == nil {
                    // the core's controller didn't answer (restarting): not the server
                    pause = 3
                } else {
                    let network = await self.networkUp()
                    guard !Task.isCancelled, self.state == .connected else { return }
                    if network == false {
                        // Wi-Fi gone, no signal: the server isn't to blame
                        fails = 0
                        self.setLink(.offline)
                        pause = 5
                    } else if network == nil {
                        pause = 3
                    } else if auto {
                        fails += 1
                        self.setLink(.checking)
                        if await self.pickWorking() {
                            pause = 0.3
                        } else {
                            self.setLink(.down)
                            pause = 15
                        }
                    } else {
                        fails += 1
                        if (first && fresh) || fails >= 2 {
                            fails = 0
                            if await self.failover(justConnected: first && fresh) {
                                self.setLink(.checking)
                                pause = 0.3
                            } else {
                                self.setLink(.down)
                                pause = 15
                            }
                        } else {
                            // once may be a hiccup: asked again soon, not in 30 s
                            self.setLink(.checking)
                            pause = 3
                        }
                    }
                }
                first = false
                try? await Task.sleep(nanoseconds: UInt64(pause * 1_000_000_000))
            }
        }
    }

    /// Delay through an outbound of the running core: ms, -1 when it didn't
    /// get through, nil when the core's controller itself didn't answer.
    private func delay(_ tag: String, url: String = TunnelManager.testURL, timeoutMs: Int) async -> Int? {
        guard port != 0, let u = URL(string: "http://127.0.0.1:\(port)/proxies/\(tag)/delay?url=\(url)&timeout=\(timeoutMs)") else { return nil }
        var req = URLRequest(url: u, timeoutInterval: Double(timeoutMs) / 1000 + 3)
        req.setValue("Bearer \(secret)", forHTTPHeaderField: "Authorization")
        return await Self.delayResult(req)
    }

    private nonisolated static func delayResult(_ req: URLRequest) async -> Int? {
        guard let (data, resp) = try? await controllerSession.data(for: req),
              let http = resp as? HTTPURLResponse else { return nil }
        // wrong secret or no such outbound: the controller, not the server
        if http.statusCode == 401 || http.statusCode == 404 { return nil }
        guard http.statusCode < 300 else { return -1 }
        let o = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        let ms = (o?["delay"] as? NSNumber)?.intValue ?? 0
        return ms > 0 ? ms : -1
    }

    /// Whether the network works without the VPN (the core's direct way out,
    /// a Russian and a foreign site): nil when the core didn't answer.
    private func networkUp() async -> Bool? {
        async let a = delay("direct", timeoutMs: 3000)
        async let b = delay("direct", url: "https%3A%2F%2Fya.ru", timeoutMs: 3000)
        let (x, y) = await (a, b)
        if x == nil, y == nil { return nil }
        return (x ?? -1) > 0 || (y ?? -1) > 0
    }

    /// Automatic selection: the server in use stopped answering. The group's
    /// next candidates (best by the last check first) are asked at once;
    /// every answer goes into the group's history and the group switches to
    /// the fastest one that answered, without waiting for its 10-minute test.
    private func pickWorking() async -> Bool {
        guard let info = await request("GET", "/proxies/auto", timeout: 3),
              let all = info["all"] as? [String], !all.isEmpty else { return false }
        let now = info["now"] as? String ?? ""
        // the one that failed starts the next connection further down the list
        if now.hasPrefix("node-") {
            let id = String(now.dropFirst(5))
            if (Store.shared.pings[id] ?? 0) > 0 { Store.shared.pings[id] = nil }
        }
        let candidates = all.filter { $0 != now }.prefix(Store.shared.state.settings.routing == "blocked" ? 4 : 6)
        return await withTaskGroup(of: Bool.self) { group in
            for tag in candidates {
                group.addTask { (await self.delay(tag, timeoutMs: 4000) ?? -1) > 0 }
            }
            for await ok in group where ok {
                group.cancelAll()
                return true
            }
            return false
        }
    }

    /// A chosen server stopped answering: automatic selection takes over
    /// (unless turned off). true when another server is in use now.
    private func failover(justConnected: Bool) async -> Bool {
        let store = Store.shared
        let st = store.state
        guard let profile = st.active else { return false }
        guard !st.settings.disableFailover else {
            if justConnected {
                error = L("Выбранный сервер не отвечает. Выберите другой сервер или «Автовыбор»")
            }
            return false
        }
        let name = profile.nodes.first { $0.id == st.selectedNode }?.name ?? L("Сервер")
        // remembered as failed, so automatic selection skips it next time
        store.pings[st.selectedNode] = -1
        store.reasons[st.selectedNode] = L("перестал отвечать во время работы")
        store.savePings()
        if await switchTo("auto", verify: false) {
            store.select(profile: profile.id, node: "auto")
            error = nil
            notice = LF("«%@» не отвечает. Vela переключилась на автовыбор", name)
            return true
        }
        if justConnected {
            error = L("Выбранный сервер не отвечает. Выберите другой сервер")
        }
        return false
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
        // through the running core when it holds this server (one request;
        // not there, it answers 404 and a temporary core checks it)
        if state == .connected, p.id == store.state.activeProfile,
           let ms = await delay(VelacoreNodeTag(n.id), timeoutMs: 6000) {
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
        // every check is a connection inside the extension (~50 MB limit):
        // fewer at once next to the big site lists of «Только заблокированное»
        let parallel = store.state.settings.routing == "blocked" ? 6 : 12
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
                    for _ in 0 ..< parallel { addNext() }
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
