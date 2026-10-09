import Foundation
import Velacore

struct Node: Codable, Identifiable, Hashable {
    let id: String
    let name: String
    let proto: String
    let server: String
    let port: Int
    let security: String
    let network: String
    let link: String
    /// Full node object from the Go core, passed back verbatim when building the config.
    let raw: String

    var isUDP: Bool { proto == "Hysteria2" || proto == "TUIC" }

    init?(dict: [String: Any]) {
        guard let id = dict["id"] as? String,
              let data = try? JSONSerialization.data(withJSONObject: dict),
              let raw = String(data: data, encoding: .utf8) else { return nil }
        self.id = id
        name = dict["name"] as? String ?? ""
        proto = dict["protocol"] as? String ?? ""
        server = dict["server"] as? String ?? ""
        port = dict["port"] as? Int ?? 0
        security = dict["security"] as? String ?? ""
        network = dict["network"] as? String ?? ""
        link = dict["link"] as? String ?? ""
        self.raw = raw
    }
}

struct Profile: Codable, Identifiable, Hashable {
    var id: String
    var name: String
    var kind: String // "subscription" | "manual"
    var url: String = ""
    var nodes: [Node] = []
    var upload: Int64 = 0
    var download: Int64 = 0
    var total: Int64 = 0
    var expire: Int64 = 0
    var intervalHours: Int = 0
    var supportURL: String = ""
    var updatedAt: Int64 = 0
    /// Name from the subscription server; customName is set by the user.
    /// Optional so state saved by older versions still decodes.
    var title: String?
    var customName: String?
}

/// User routing rule: domain, *keyword* or IP/CIDR → direct, proxy or block.
struct RoutingRule: Codable, Equatable, Identifiable {
    var id = UUID().uuidString
    var match: String
    var action: String = "direct"

    enum CodingKeys: String, CodingKey { case match, action }
}

struct VelaSettings: Codable, Equatable {
    var routing = "smart"
    var blockAds = false
    var ipv6 = true
    var remoteDNS = "https://1.1.1.1/dns-query"
    var directDNS = "77.88.8.8"
    var directDomains: [String] = []
    var proxyDomains: [String] = []
    /// Switch to a working server when the chosen one stops answering.
    var disableFailover = false
    /// "" off, "record" light, "full" strongest TLS handshake splitting.
    var tlsFragment = ""
    var rules: [RoutingRule] = []

    init() {}

    // New fields are optional in saved state, so older versions' files still load.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let d = VelaSettings.defaults
        routing = try c.decodeIfPresent(String.self, forKey: .routing) ?? d.routing
        blockAds = try c.decodeIfPresent(Bool.self, forKey: .blockAds) ?? d.blockAds
        ipv6 = try c.decodeIfPresent(Bool.self, forKey: .ipv6) ?? d.ipv6
        remoteDNS = try c.decodeIfPresent(String.self, forKey: .remoteDNS) ?? d.remoteDNS
        directDNS = try c.decodeIfPresent(String.self, forKey: .directDNS) ?? d.directDNS
        directDomains = try c.decodeIfPresent([String].self, forKey: .directDomains) ?? []
        proxyDomains = try c.decodeIfPresent([String].self, forKey: .proxyDomains) ?? []
        disableFailover = try c.decodeIfPresent(Bool.self, forKey: .disableFailover) ?? false
        tlsFragment = try c.decodeIfPresent(String.self, forKey: .tlsFragment) ?? ""
        rules = try c.decodeIfPresent([RoutingRule].self, forKey: .rules) ?? []
    }

    static let defaults = VelaSettings()

    var json: [String: Any] {
        ["mode": "tun", "routing": routing, "blockAds": blockAds, "ipv6": ipv6, "stack": "system",
         "remoteDns": remoteDNS, "directDns": directDNS, "directDomains": directDomains,
         "proxyDomains": proxyDomains, "logLevel": "warn", "disableFailover": disableFailover,
         "tlsFragment": tlsFragment,
         "rules": rules.filter { !$0.match.isEmpty }.map { ["match": $0.match, "action": $0.action] }]
    }

    /// Reads settings exported by any Vela app (keys of the Go core).
    init(core o: [String: Any], keeping cur: VelaSettings) {
        self = cur
        routing = o["routing"] as? String ?? cur.routing
        blockAds = o["blockAds"] as? Bool ?? cur.blockAds
        ipv6 = o["ipv6"] as? Bool ?? cur.ipv6
        remoteDNS = (o["remoteDns"] as? String).flatMap { $0.isEmpty ? nil : $0 } ?? cur.remoteDNS
        directDNS = (o["directDns"] as? String).flatMap { $0.isEmpty ? nil : $0 } ?? cur.directDNS
        directDomains = o["directDomains"] as? [String] ?? cur.directDomains
        proxyDomains = o["proxyDomains"] as? [String] ?? cur.proxyDomains
        disableFailover = o["disableFailover"] as? Bool ?? false
        tlsFragment = o["tlsFragment"] as? String ?? ""
        rules = (o["rules"] as? [[String: Any]] ?? []).compactMap { r in
            guard let m = r["match"] as? String, !m.isEmpty else { return nil }
            return RoutingRule(match: m, action: r["action"] as? String ?? "direct")
        }
    }
}

/// Subscription settings. Zero values are the defaults.
struct SubPrefs: Codable, Equatable {
    var noAutoUpdate = false
    /// 0: as the provider says (12 h if it doesn't).
    var updateHours = 0
    var noHWID = false
    var userAgent = ""

    init() {}
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        noAutoUpdate = try c.decodeIfPresent(Bool.self, forKey: .noAutoUpdate) ?? false
        updateHours = try c.decodeIfPresent(Int.self, forKey: .updateHours) ?? 0
        noHWID = try c.decodeIfPresent(Bool.self, forKey: .noHWID) ?? false
        userAgent = try c.decodeIfPresent(String.self, forKey: .userAgent) ?? ""
    }
}

struct AppState: Codable {
    var profiles: [Profile] = []
    var activeProfile = ""
    var selectedNode = "auto"
    var settings = VelaSettings()
    /// Favorite server ids; automatic selection prefers them.
    var favorites: [String] = []
    /// "system", "light" or "dark".
    var theme = "system"
    var sub = SubPrefs()
    /// Random id of this installation, sent as HWID when enabled.
    var hwid = ""
    var connectOnLaunch = false
    /// iOS reconnects the VPN by itself (On Demand) until it's turned off in Vela.
    var alwaysOn = false
    var sortByPing = false

    var active: Profile? { profiles.first { $0.id == activeProfile } ?? profiles.first }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        profiles = try c.decodeIfPresent([Profile].self, forKey: .profiles) ?? []
        activeProfile = try c.decodeIfPresent(String.self, forKey: .activeProfile) ?? ""
        selectedNode = try c.decodeIfPresent(String.self, forKey: .selectedNode) ?? "auto"
        settings = try c.decodeIfPresent(VelaSettings.self, forKey: .settings) ?? VelaSettings()
        favorites = try c.decodeIfPresent([String].self, forKey: .favorites) ?? []
        theme = try c.decodeIfPresent(String.self, forKey: .theme) ?? "system"
        sub = try c.decodeIfPresent(SubPrefs.self, forKey: .sub) ?? SubPrefs()
        hwid = try c.decodeIfPresent(String.self, forKey: .hwid) ?? ""
        connectOnLaunch = try c.decodeIfPresent(Bool.self, forKey: .connectOnLaunch) ?? false
        alwaysOn = try c.decodeIfPresent(Bool.self, forKey: .alwaysOn) ?? false
        sortByPing = try c.decodeIfPresent(Bool.self, forKey: .sortByPing) ?? false
    }
}

struct CoreError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

/// Holds a result written from the core's thread.
private final class ResultBox: @unchecked Sendable {
    private let lock = NSLock()
    private var value: (Int, String)?
    func set(_ v: (Int, String)) { lock.lock(); value = v; lock.unlock() }
    func get() -> (Int, String)? { lock.lock(); defer { lock.unlock() }; return value }
}

/// Bridges Go probe callbacks to Swift.
private final class ProbeCallback: NSObject, VelacoreProbeHandlerProtocol {
    let handler: (String, Int, String) -> Void
    init(_ handler: @escaping (String, Int, String) -> Void) { self.handler = handler }
    func onResult(_ nodeID: String?, delayMs: Int32, reason: String?, rawError _: String?) {
        handler(nodeID ?? "", Int(delayMs), reason ?? "")
    }
}

@MainActor
final class Store: ObservableObject {
    static let shared = Store()
    static let manualID = "manual"

    @Published private(set) var state = AppState()
    /// ms per node id; -1 failed, 0 measuring
    @Published var pings: [String: Int] = [:]
    /// why a node failed the last check
    @Published var reasons: [String: String] = [:]
    /// progress of the server check
    @Published var probe: (done: Int, total: Int)?

    let dataURL: URL
    private let fileURL: URL

    private init() {
        // App Group is optional: re-signed builds often lose it.
        let group = Bundle.main.object(forInfoDictionaryKey: "AppGroupIdentifier") as? String ?? ""
        let shared = group.isEmpty ? nil : FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group)
        dataURL = shared ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Vela", isDirectory: true)
        try? FileManager.default.createDirectory(at: dataURL, withIntermediateDirectories: true)
        fileURL = dataURL.appendingPathComponent("state.json")
        if let data = try? Data(contentsOf: fileURL), var st = try? JSONDecoder().decode(AppState.self, from: data) {
            // duplicates saved by older versions
            for i in st.profiles.indices {
                var seen = Set<String>()
                st.profiles[i].nodes = st.profiles[i].nodes.filter { seen.insert($0.id).inserted }
            }
            state = st
        }
        if state.hwid.isEmpty {
            state.hwid = Self.randomID(16)
            persist(state)
        }
        applyFetchOptions()
        if let data = try? Data(contentsOf: dataURL.appendingPathComponent("pings.json")),
           let saved = try? JSONDecoder().decode([String: Int].self, from: data) {
            pings = saved.filter { $0.value != 0 }
        }
    }

    /// Remembers check results so automatic selection skips dead servers after a restart.
    func savePings() {
        if let data = try? JSONEncoder().encode(pings.filter { $0.value != 0 }) {
            try? data.write(to: dataURL.appendingPathComponent("pings.json"), options: .atomic)
        }
    }

    private func update(_ change: (inout AppState) -> Void) {
        var next = state
        change(&next)
        state = next
        persist(next)
    }

    private func persist(_ next: AppState) {
        if let data = try? JSONEncoder().encode(next) {
            // keys stay out of iCloud/iTunes backups
            try? data.write(to: fileURL, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
            var url = fileURL
            var values = URLResourceValues()
            values.isExcludedFromBackup = true
            try? url.setResourceValues(values)
        }
    }

    // MARK: - Import

    /// Imports any mix of subscription URLs, share links and base64 bodies.
    /// Several subscriptions can be pasted at once, one per line.
    func importText(_ text: String) async throws -> String {
        let input = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !input.isEmpty else { throw CoreError(message: "Вставьте ссылку на подписку или сервер") }
        var backupError: NSError?
        let backup = VelacoreDecodeBackup(input, &backupError)
        if let backupError { throw CoreError(message: backupError.localizedDescription) }
        if !backup.isEmpty { return try await restore(Self.json(backup)) }
        let split = Self.json(VelacoreSplitImport(input))
        let urls = split["subscriptions"] as? [String] ?? []
        let linksText = split["links"] as? String ?? ""
        var parts: [String] = []
        var failures: [String] = []
        var lastProfile: String?

        let fetched: [(String, Result<[String: Any], Error>)] = await withTaskGroup(of: (Int, String, Result<[String: Any], Error>).self) { group in
            for (i, url) in urls.enumerated() {
                group.addTask {
                    do { return (i, url, .success(try await Self.fetchSubscription(url))) } catch { return (i, url, .failure(error)) }
                }
            }
            var out: [(Int, String, Result<[String: Any], Error>)] = []
            for await item in group { out.append(item) }
            return out.sorted { $0.0 < $1.0 }.map { ($0.1, $0.2) }
        }

        for (url, result) in fetched {
            switch result {
            case let .failure(error):
                failures.append((URL(string: url)?.host ?? url) + ": " + error.localizedDescription)
            case let .success(sub):
                let nodes = Self.nodes(sub["nodes"])
                var label = ""
                var isNew = false
                update { st in
                    let existing = st.profiles.firstIndex { $0.kind == "subscription" && $0.url == url }
                    isNew = existing == nil
                    var profile = Self.makeProfile(id: existing.map { st.profiles[$0].id } ?? Self.randomID(), url: url, sub: sub, nodes: nodes,
                                                   customName: existing.flatMap { st.profiles[$0].customName })
                    profile.name = Self.uniqueName(st.profiles, profile)
                    label = profile.name
                    lastProfile = profile.id
                    if let i = existing { st.profiles[i] = profile } else { st.profiles.append(profile) }
                }
                parts.append(isNew ? "«\(label)»: серверов \(nodes.count)" : "«\(label)» обновлена")
            }
        }

        if !linksText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            let parsed = Self.json(VelacoreParseText(linksText))
            let nodes = Self.nodes(parsed["nodes"])
            let errors = parsed["errors"] as? [String] ?? []
            if nodes.isEmpty {
                failures.append(errors.first ?? "не нашли поддерживаемых ссылок")
            } else {
                var added = 0
                update { st in
                    var manual = st.profiles.first { $0.id == Self.manualID } ?? Profile(id: Self.manualID, name: "Мои серверы", kind: "manual")
                    let known = Set(manual.nodes.map(\.link))
                    let fresh = nodes.filter { !known.contains($0.link) }
                    added = fresh.count
                    manual.nodes += fresh
                    if let i = st.profiles.firstIndex(where: { $0.id == Self.manualID }) { st.profiles[i] = manual } else { st.profiles.insert(manual, at: 0) }
                }
                lastProfile = Self.manualID
                parts.append(added == 0 ? "эти серверы уже есть в списке" : "серверов добавлено: \(added)")
                if !errors.isEmpty { parts.append("пропущено ссылок: \(errors.count)") }
            }
        }

        guard let target = lastProfile else {
            throw CoreError(message: failures.isEmpty ? "Не нашли поддерживаемых ссылок" : failures.joined(separator: "; "))
        }
        update { st in
            // the first import becomes active; later ones are added alongside
            if st.activeProfile.isEmpty || !st.profiles.contains(where: { $0.id == st.activeProfile }) {
                st.activeProfile = target
                st.selectedNode = "auto"
            }
        }
        var msg = parts.joined(separator: ", ")
        if !failures.isEmpty { msg += ". Не удалось: " + failures.joined(separator: "; ") }
        return msg.prefix(1).uppercased() + msg.dropFirst()
    }

    func refresh(_ profileID: String) async throws {
        guard let p = state.profiles.first(where: { $0.id == profileID && $0.kind == "subscription" }) else {
            throw CoreError(message: "Подписка не найдена")
        }
        let sub = try await Self.fetchSubscription(p.url)
        let nodes = Self.nodes(sub["nodes"])
        update { st in
            if let i = st.profiles.firstIndex(where: { $0.id == p.id }) {
                var fresh = Self.makeProfile(id: p.id, url: p.url, sub: sub, nodes: nodes, customName: p.customName)
                fresh.name = Self.uniqueName(st.profiles, fresh)
                st.profiles[i] = fresh
            }
        }
    }

    func refreshAll() async throws -> String {
        let ids = state.profiles.filter { $0.kind == "subscription" }.map(\.id)
        guard !ids.isEmpty else { throw CoreError(message: "Подписок пока нет") }
        var failed = 0
        for id in ids {
            do { try await refresh(id) } catch { failed += 1 }
        }
        if failed == ids.count { throw CoreError(message: "Не удалось обновить подписки, проверьте интернет") }
        return failed > 0 ? "Обновлено \(ids.count - failed) из \(ids.count) подписок" : "Все подписки обновлены"
    }

    func refreshDue() async {
        if state.sub.noAutoUpdate { return }
        let now = Int64(Date().timeIntervalSince1970)
        for p in state.profiles where p.kind == "subscription" {
            let hours = state.sub.updateHours > 0 ? state.sub.updateHours : (p.intervalHours > 0 ? p.intervalHours : 12)
            let interval = Int64(hours)
            if now - p.updatedAt > interval * 3600 { try? await refresh(p.id) }
        }
    }

    private nonisolated static func fetchSubscription(_ url: String) async throws -> [String: Any] {
        try await Task.detached(priority: .userInitiated) {
            var error: NSError?
            let result = VelacoreFetchSubscription(url, &error)
            if let error { throw CoreError(message: error.localizedDescription) }
            return Self.json(result)
        }.value
    }

    // MARK: - Mutations

    func rename(_ profileID: String, to name: String) {
        update { st in
            guard let i = st.profiles.firstIndex(where: { $0.id == profileID }) else { return }
            let custom = name.trimmingCharacters(in: .whitespacesAndNewlines)
            st.profiles[i].customName = custom.isEmpty ? nil : custom
            st.profiles[i].name = custom.isEmpty ? (st.profiles[i].title ?? st.profiles[i].name) : custom
            st.profiles[i].name = Self.uniqueName(st.profiles, st.profiles[i])
        }
    }

    func deleteProfile(_ id: String) {
        update { st in
            st.profiles.removeAll { $0.id == id }
            if st.activeProfile == id {
                st.activeProfile = st.profiles.first?.id ?? ""
                st.selectedNode = "auto"
            }
        }
    }

    func deleteNode(profile: String, node: String) {
        update { st in
            if let i = st.profiles.firstIndex(where: { $0.id == profile }) { st.profiles[i].nodes.removeAll { $0.id == node } }
            if st.selectedNode == node { st.selectedNode = "auto" }
        }
    }

    func select(profile: String, node: String) {
        update { st in
            st.activeProfile = profile
            st.selectedNode = node
        }
    }

    func setSettings(_ s: VelaSettings) { update { $0.settings = s } }

    func setFavorite(_ id: String, _ on: Bool) {
        update { st in
            st.favorites.removeAll { $0 == id }
            if on { st.favorites.append(id) }
        }
    }

    func setTheme(_ theme: String) { update { $0.theme = theme } }

    func setSubPrefs(_ p: SubPrefs) {
        var p = p
        p.userAgent = p.userAgent.trimmingCharacters(in: .whitespacesAndNewlines)
        update { $0.sub = p }
        applyFetchOptions()
    }

    func setConnectOnLaunch(_ on: Bool) { update { $0.connectOnLaunch = on } }
    func setAlwaysOn(_ on: Bool) { update { $0.alwaysOn = on } }
    func setSortByPing(_ on: Bool) { update { $0.sortByPing = on } }

    /// Settings back to defaults; subscriptions, servers and favorites stay.
    func resetSettings() {
        update { st in
            st.settings = VelaSettings()
            st.theme = "system"
            st.sub = SubPrefs()
            st.connectOnLaunch = false
            st.alwaysOn = false
        }
        applyFetchOptions()
    }

    /// Passes the subscription settings to the core.
    private func applyFetchOptions() {
        var o: [String: Any] = ["userAgent": state.sub.userAgent]
        if !state.sub.noHWID {
            var sys = utsname()
            uname(&sys)
            let model = withUnsafeBytes(of: &sys.machine) { raw in
                String(decoding: raw.prefix { $0 != 0 }, as: UTF8.self)
            }
            o["hwid"] = state.hwid
            o["deviceOs"] = "iOS"
            o["osVersion"] = ProcessInfo.processInfo.operatingSystemVersionString
                .replacingOccurrences(of: "Version ", with: "").components(separatedBy: " ").first ?? ""
            o["deviceModel"] = model
        }
        if let data = try? JSONSerialization.data(withJSONObject: o), let text = String(data: data, encoding: .utf8) {
            VelacoreSetSubscriptionOptions(text)
        }
    }

    // MARK: - Backup

    /// Everything needed to set Vela up on another device, as text for a QR code.
    func exportBackup() throws -> String {
        let st = state
        let subs = st.profiles.filter { $0.kind == "subscription" }.map { ["url": $0.url, "name": $0.customName ?? ""] }
        let links = st.profiles.first { $0.id == Self.manualID }?.nodes.map(\.link).filter { !$0.isEmpty } ?? []
        let favs = st.profiles.flatMap(\.nodes).filter { st.favorites.contains($0.id) }.map(\.link).filter { !$0.isEmpty }
        let b: [String: Any] = ["subscriptions": subs, "links": links, "favorites": favs, "settings": st.settings.json,
                                "extra": ["theme": st.theme, "platform": "ios"]]
        let data = try JSONSerialization.data(withJSONObject: b)
        var error: NSError?
        let text = VelacoreEncodeBackup(String(data: data, encoding: .utf8), &error)
        if let error { throw CoreError(message: error.localizedDescription) }
        return text
    }

    private func restore(_ b: [String: Any]) async throws -> String {
        let subs = b["subscriptions"] as? [[String: Any]] ?? []
        var lines = subs.compactMap { $0["url"] as? String }.filter { !$0.isEmpty }
        lines += b["links"] as? [String] ?? []
        var msg = ""
        var importError: String?
        if !lines.isEmpty {
            // settings are restored even when the subscriptions can't be downloaded now
            do { msg = try await importText(lines.joined(separator: "\n")) } catch { importError = error.localizedDescription }
        }
        for s in subs {
            guard let name = s["name"] as? String, !name.isEmpty, let url = s["url"] as? String,
                  let p = state.profiles.first(where: { $0.kind == "subscription" && $0.url == url }) else { continue }
            rename(p.id, to: name)
        }
        let favLinks = Set(b["favorites"] as? [String] ?? [])
        let extra = b["extra"] as? [String: Any] ?? [:]
        update { st in
            if let o = b["settings"] as? [String: Any] { st.settings = VelaSettings(core: o, keeping: st.settings) }
            for n in st.profiles.flatMap(\.nodes) where favLinks.contains(n.link) && !st.favorites.contains(n.id) {
                st.favorites.append(n.id)
            }
            if let t = extra["theme"] as? String, ["system", "light", "dark"].contains(t) { st.theme = t }
        }
        if let importError { return "Настройки восстановлены. Подписки не загрузились: " + importError }
        return msg.isEmpty ? "Настройки восстановлены" : "Восстановлено. " + msg
    }

    // MARK: - Server check

    /// Real request through every server using a temporary core in the app.
    /// Call while the VPN is off; reasons for failures are reported.
    /// Set by «Остановить»: failures that arrive after it are not real.
    nonisolated(unsafe) var stopRequested = false

    func probeOffline(_ profile: Profile) async {
        guard probe == nil else { return }
        stopRequested = false
        for n in profile.nodes { pings[n.id] = 0; reasons[n.id] = nil }
        probe = (0, profile.nodes.count)
        await probeWithTempCore(profile.nodes)
        try? await Task.sleep(nanoseconds: 200_000_000)
        // stopped: servers that weren't checked must not stay "measuring"
        for n in profile.nodes where pings[n.id] == 0 { pings[n.id] = nil }
        probe = nil
        savePings()
    }

    /// Real request through each server with a temporary core in the app.
    /// Updates pings, reasons and the progress counter.
    func probeWithTempCore(_ nodes: [Node]) async {
        guard !nodes.isEmpty else { return }
        let nodesJSON = "[" + nodes.map(\.raw).joined(separator: ",") + "]"
        let callback = ProbeCallback { id, ms, reason in
            Task { @MainActor in
                let s = Store.shared
                if ms <= 0, s.stopRequested { return }
                s.pings[id] = ms > 0 ? ms : -1
                if ms <= 0, !reason.isEmpty { s.reasons[id] = reason }
                if let p = s.probe { s.probe = (p.done + 1, p.total) }
            }
        }
        let failed: Bool = await Task.detached(priority: .userInitiated) {
            var err: NSError?
            let ok = VelacoreProbeNodes(nodesJSON, 9000, 12, callback, &err)
            return !ok || err != nil
        }.value
        if failed, !stopRequested {
            // temporary core unavailable: fall back to TCP reachability
            for n in nodes where (pings[n.id] ?? 0) == 0 {
                let ms = await Task.detached { Int(VelacoreTCPPing(n.server, Int32(n.port))) }.value
                pings[n.id] = ms > 0 ? ms : -1
                if ms <= 0 { reasons[n.id] = "сервер недоступен" }
            }
        }
    }

    /// Checks one server with a temporary core (VPN off).
    func probeOne(_ node: Node) async {
        pings[node.id] = 0
        reasons[node.id] = nil
        let box = ResultBox()
        let callback = ProbeCallback { _, ms, reason in box.set((ms, reason)) }
        let json = "[" + node.raw + "]"
        _ = await Task.detached(priority: .userInitiated) {
            var err: NSError?
            return VelacoreProbeNodes(json, 9000, 1, callback, &err)
        }.value
        guard let (ms, reason) = box.get() else {
            pings[node.id] = nil // stopped
            return
        }
        pings[node.id] = ms > 0 ? ms : -1
        if ms <= 0, !reason.isEmpty { reasons[node.id] = reason }
        savePings()
    }

    // MARK: - Config

    /// Builds the sing-box config. Paths point to a placeholder that the
    /// Network Extension replaces with its own data directory, so the app and
    /// the extension don't need a shared container.
    func buildConfig(port: Int, secret: String) throws -> String {
        guard let profile = state.active, !profile.nodes.isEmpty else {
            throw CoreError(message: "Сначала добавьте сервер или подписку")
        }
        let nodes = profile.nodes.compactMap { Self.json($0.raw) as Any? }
        let ids = Set(profile.nodes.map(\.id))
        // automatic selection: favorites first, then the fastest, never the dead ones
        let ranked = pings.filter { ids.contains($0.key) && $0.value > 0 }.sorted { $0.value < $1.value }.map(\.key)
        let failed = pings.filter { ids.contains($0.key) && $0.value < 0 }.map(\.key)
        let req: [String: Any] = [
            "platform": "ios", "nodes": nodes,
            "selected": profile.id == state.activeProfile ? state.selectedNode : "auto",
            "settings": state.settings.json, "dataDir": "__VELA_DATA__", "skipRuleInstall": true,
            "controllerPort": port, "controllerSecret": secret,
            "favorites": state.favorites.filter { ids.contains($0) }, "ranked": ranked, "failed": failed,
            "logPath": "__VELA_DATA__/core.log",
        ]
        let data = try JSONSerialization.data(withJSONObject: req)
        var error: NSError?
        let config = VelacoreBuildConfig(String(data: data, encoding: .utf8), &error)
        if let error { throw CoreError(message: error.localizedDescription) }
        return config
    }

    // MARK: - Helpers

    nonisolated static func json(_ s: String) -> [String: Any] {
        guard let data = s.data(using: .utf8), let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [:] }
        return obj
    }

    nonisolated static func nodes(_ value: Any?) -> [Node] {
        // the same server twice would break lists (duplicate ids) and the config
        var seen = Set<String>()
        return (value as? [[String: Any]] ?? []).compactMap(Node.init(dict:)).filter { seen.insert($0.id).inserted }
    }

    nonisolated static func makeProfile(id: String, url: String, sub: [String: Any], nodes: [Node], customName: String?) -> Profile {
        func i64(_ k: String) -> Int64 { (sub[k] as? NSNumber)?.int64Value ?? 0 }
        let title = (sub["title"] as? String).flatMap { $0.isEmpty ? nil : $0 }
        var p = Profile(id: id, name: customName ?? title ?? (URL(string: url)?.host ?? url), kind: "subscription", url: url)
        p.title = title
        p.customName = customName
        p.nodes = nodes
        p.upload = i64("upload"); p.download = i64("download"); p.total = i64("total"); p.expire = i64("expire")
        p.updatedAt = i64("updatedAt")
        p.intervalHours = (sub["updateIntervalHours"] as? NSNumber)?.intValue ?? 0
        let support = (sub["supportUrl"] as? String) ?? ""
        p.supportURL = support.isEmpty ? ((sub["webPageUrl"] as? String) ?? "") : support
        return p
    }

    /// Keeps profile names unique so tabs can be told apart.
    nonisolated static func uniqueName(_ all: [Profile], _ p: Profile) -> String {
        if let custom = p.customName, !custom.isEmpty { return custom }
        let base = p.name.isEmpty ? (URL(string: p.url)?.host ?? "Подписка") : p.name
        var name = base
        var n = 2
        while all.contains(where: { $0.id != p.id && $0.name == name }) {
            name = "\(base) \(n)"
            n += 1
        }
        return name
    }

    nonisolated static func randomID(_ bytes: Int = 8) -> String {
        (0 ..< bytes).map { _ in String(format: "%02x", UInt8.random(in: 0 ... 255)) }.joined()
    }
}
