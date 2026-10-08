import Foundation
import NetworkExtension
import Velacore

enum ConnState { case disconnected, connecting, connected, disconnecting }

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
        switch status {
        case .connected:
            state = .connected
            since = manager?.connection.connectedDate ?? Date()
            startStats()
        case .connecting, .reasserting:
            state = .connecting
        case .disconnecting:
            state = .disconnecting
        default:
            state = .disconnected
            since = nil
            up = 0; down = 0; total = 0; activeNode = nil
            statsTask?.cancel()
        }
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
        // iOS asks the user to allow adding the VPN configuration here the first time.
        try await m.saveToPreferences()
        try await m.loadFromPreferences()
        manager = m
        observe(m)
        return m
    }

    func connect() async {
        error = nil
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
        manager?.connection.stopVPNTunnel()
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
                if let last { self.up = max(0, u - last.0); self.down = max(0, d - last.1) }
                last = (u, d)
                self.total = u + d
                if tick % 3 == 0 { self.activeNode = await self.resolveActive() }
                tick += 1
            }
        }
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
        await withTaskGroup(of: (String, Int).self) { group in
            var started = 0
            var iterator = profile.nodes.makeIterator()
            func addNext() {
                guard let n = iterator.next() else { return }
                started += 1
                group.addTask {
                    let path = "/proxies/\(VelacoreNodeTag(n.id))/delay?url=https%3A%2F%2Fwww.gstatic.com%2Fgenerate_204&timeout=8000"
                    let r = await self.request("GET", path, timeout: 11)
                    return (n.id, (r?["delay"] as? NSNumber)?.intValue ?? -1)
                }
            }
            for _ in 0 ..< 12 { addNext() }
            for await (id, ms) in group {
                store.pings[id] = ms > 0 ? ms : -1
                if ms <= 0 { store.reasons[id] = "сервер не ответил" }
                if let p = store.probe { store.probe = (p.done + 1, p.total) }
                addNext()
            }
        }
        store.probe = nil
    }
}
