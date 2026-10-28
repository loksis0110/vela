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
    /// "xray" for servers that run on the embedded Xray engine (XHTTP, mKCP,
    /// Xray JSON). Optional so servers saved by older versions still decode.
    let engine: String?
    /// Full node object from the Go core, passed back verbatim when building the config.
    let raw: String

    var isUDP: Bool { proto == "Hysteria2" || proto == "TUIC" }
    var isXray: Bool { engine == "xray" }

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
        engine = (dict["engine"] as? String).flatMap { $0.isEmpty ? nil : $0 }
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
    /// Message from the provider (announce header or #announce: line).
    var announce: String?

    /// The name to show; the list of own servers follows the app language.
    var displayName: String { id == "manual" ? L("Мои серверы") : name }
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
    /// Offer subscriptions copied to the clipboard.
    var clipboardCheck = true
    /// Notifications about the end of a subscription and its traffic.
    var reminders = true

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
        clipboardCheck = try c.decodeIfPresent(Bool.self, forKey: .clipboardCheck) ?? true
        reminders = try c.decodeIfPresent(Bool.self, forKey: .reminders) ?? true
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

/// Results of a server check from the core's threads. The UI takes them a
/// few times a second: hundreds arrive at once (dead hosts fail within
/// seconds), and redrawing the list for each one made it freeze.
final class ProbeFeed: @unchecked Sendable {
    struct Item {
        let id: String
        let ms: Int
        let reason: String
    }

    private let lock = NSLock()
    private var items: [Item] = []

    func add(_ id: String, _ ms: Int, _ reason: String) {
        lock.lock()
        items.append(Item(id: id, ms: ms, reason: reason))
        lock.unlock()
    }

    func take() -> [Item] {
        lock.lock()
        defer { lock.unlock() }
        let out = items
        items = []
        return out
    }
}

/// Numbers of queued writes per file: an older copy isn't written when a
/// newer one is already queued.
final class WriteSeq: @unchecked Sendable {
    private let lock = NSLock()
    private var last: [String: Int] = [:]

    func next(_ key: String) -> Int {
        lock.lock()
        defer { lock.unlock() }
        let n = (last[key] ?? 0) + 1
        last[key] = n
        return n
    }

    func isLatest(_ key: String, _ n: Int) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return last[key] == n
    }
}

/// A flag read from the check's threads (set by «Остановить»).
final class StopFlag: @unchecked Sendable {
    private let lock = NSLock()
    private var on = false
    var value: Bool {
        get { lock.lock(); defer { lock.unlock() }; return on }
        set { lock.lock(); on = newValue; lock.unlock() }
    }
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
    /// progress of the server check; ok: working so far
    @Published var probe: (done: Int, total: Int, ok: Int)?
    /// Provider announcements the user closed: profile id → text.
    @Published private(set) var dismissedAnnounce: [String: String] =
        UserDefaults.standard.dictionary(forKey: "dismissedAnnounce") as? [String: String] ?? [:]

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
        if let data = try? Data(contentsOf: fileURL) {
            if var st = try? JSONDecoder().decode(AppState.self, from: data) {
                // duplicates saved by older versions
                for i in st.profiles.indices {
                    var seen = Set<String>()
                    st.profiles[i].nodes = st.profiles[i].nodes.filter { seen.insert($0.id).inserted }
                }
                state = st
            } else {
                // never overwrite subscriptions that couldn't be read: keep a copy
                try? data.write(to: dataURL.appendingPathComponent("state-unreadable.json"), options: .atomic)
            }
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
        let snapshot = pings.filter { $0.value != 0 }
        let url = dataURL.appendingPathComponent("pings.json")
        let seq = writes.next("pings")
        Self.io.async { [writes] in
            // a newer copy is already on its way
            guard writes.isLatest("pings", seq), let data = try? JSONEncoder().encode(snapshot) else { return }
            try? data.write(to: url, options: .atomic)
        }
    }

    /// Files are written off the main thread, one after another: encoding
    /// hundreds of servers took a noticeable moment on every tap.
    private nonisolated static let io = DispatchQueue(label: "app.vela.store.io", qos: .utility)
    private nonisolated let writes = WriteSeq()

    /// Waits until everything changed so far is on disk (the app goes to the
    /// background and may be closed).
    func flush() {
        Self.io.sync {}
    }

    private func update(_ change: (inout AppState) -> Void) {
        var next = state
        change(&next)
        state = next
        persist(next)
    }

    private func persist(_ next: AppState) {
        let target = fileURL
        let seq = writes.next("state")
        Self.io.async { [writes] in
            // several changes in a row: only the newest state is written
            guard writes.isLatest("state", seq), let data = try? JSONEncoder().encode(next) else { return }
            // keys stay out of iCloud/iTunes backups
            try? data.write(to: target, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
            var url = target
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
        guard !input.isEmpty else { throw CoreError(message: L("Вставьте ссылку на подписку или сервер")) }
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
                parts.append(isNew ? LF("«%@»: серверов %@", label, "\(nodes.count)") : LF("«%@» обновлена", label))
            }
        }

        if !linksText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            let parsed = Self.json(VelacoreParseText(linksText))
            let nodes = Self.nodes(parsed["nodes"])
            let errors = parsed["errors"] as? [String] ?? []
            if nodes.isEmpty {
                failures.append(errors.first ?? L("не нашли поддерживаемых ссылок"))
            } else {
                var added = 0
                update { st in
                    var manual = st.profiles.first { $0.id == Self.manualID } ?? Profile(id: Self.manualID, name: L("Мои серверы"), kind: "manual")
                    let known = Set(manual.nodes.map(\.link))
                    let fresh = nodes.filter { !known.contains($0.link) }
                    added = fresh.count
                    manual.nodes += fresh
                    if let i = st.profiles.firstIndex(where: { $0.id == Self.manualID }) { st.profiles[i] = manual } else { st.profiles.insert(manual, at: 0) }
                }
                lastProfile = Self.manualID
                parts.append(added == 0 ? L("эти серверы уже есть в списке") : LF("серверов добавлено: %@", "\(added)"))
                if !errors.isEmpty { parts.append(LF("пропущено ссылок: %@", "\(errors.count)")) }
            }
        }

        guard let target = lastProfile else {
            throw CoreError(message: failures.isEmpty ? L("Не нашли поддерживаемых ссылок") : failures.joined(separator: "; "))
        }
        update { st in
            // the first import becomes active; later ones are added alongside
            if st.activeProfile.isEmpty || !st.profiles.contains(where: { $0.id == st.activeProfile }) {
                st.activeProfile = target
                st.selectedNode = "auto"
            }
        }
        var msg = parts.joined(separator: ", ")
        if !failures.isEmpty { msg += ". " + L("Не удалось:") + " " + failures.joined(separator: "; ") }
        Reminders.shared.sync(ask: true)
        return msg.prefix(1).uppercased() + msg.dropFirst()
    }

    /// The part of an offer (app link, clipboard) that isn't added yet: new
    /// subscription URLs and servers not in any list. nil when all is there.
    func newPart(of offer: ImportOffer) -> ImportOffer? {
        let urls = Set(state.profiles.filter { $0.kind == "subscription" }.map(\.url))
        let links = Set(state.profiles.flatMap(\.nodes).map(\.link))
        var o = offer
        o.subscriptions = offer.subscriptions.filter { !urls.contains($0) }
        let fresh = offer.links.split(separator: "\n").map(String.init).filter { !$0.isEmpty && !links.contains($0) }
        o.links = fresh.joined(separator: "\n")
        o.linkCount = fresh.count
        return o.subscriptions.isEmpty && o.links.isEmpty ? nil : o
    }

    /// Adds what an app link or the clipboard offered through the usual
    /// import; the name from the link is used when the provider sends none.
    func importOffer(_ offer: ImportOffer) async throws -> String {
        let msg = try await importText(offer.text)
        if !offer.name.isEmpty, offer.subscriptions.count == 1,
           let p = state.profiles.first(where: { $0.kind == "subscription" && $0.url == offer.subscriptions[0] }),
           p.title == nil, p.customName == nil {
            rename(p.id, to: offer.name)
        }
        return msg
    }

    // MARK: - Announcements

    /// The provider's message for a profile, unless the user closed this text.
    func announcement(for p: Profile?) -> String? {
        guard let p, let text = p.announce?.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty,
              dismissedAnnounce[p.id] != text else { return nil }
        return text
    }

    func dismissAnnouncement(_ p: Profile) {
        dismissedAnnounce[p.id] = p.announce?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let ids = Set(state.profiles.map(\.id))
        dismissedAnnounce = dismissedAnnounce.filter { ids.contains($0.key) }
        UserDefaults.standard.set(dismissedAnnounce, forKey: "dismissedAnnounce")
    }

    func refresh(_ profileID: String) async throws {
        guard let p = state.profiles.first(where: { $0.id == profileID && $0.kind == "subscription" }) else {
            throw CoreError(message: L("Подписка не найдена"))
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
        Reminders.shared.sync()
    }

    func refreshAll() async throws -> String {
        let ids = state.profiles.filter { $0.kind == "subscription" }.map(\.id)
        guard !ids.isEmpty else { throw CoreError(message: L("Подписок пока нет")) }
        var failed = 0
        for id in ids {
            do { try await refresh(id) } catch { failed += 1 }
        }
        if failed == ids.count { throw CoreError(message: L("Не удалось обновить подписки, проверьте интернет")) }
        return failed > 0 ? LF("Обновлено подписок: %@ из %@", "\(ids.count - failed)", "\(ids.count)") : L("Все подписки обновлены")
    }

    /// Refreshes subscriptions whose time has come. true when the servers of
    /// the active one changed (the running tunnel needs the new list).
    @discardableResult
    func refreshDue() async -> Bool {
        if state.sub.noAutoUpdate { return false }
        let before = activeServers
        let now = Int64(Date().timeIntervalSince1970)
        for p in state.profiles where p.kind == "subscription" {
            let hours = state.sub.updateHours > 0 ? state.sub.updateHours : (p.intervalHours > 0 ? p.intervalHours : 12)
            let interval = Int64(hours)
            if now - p.updatedAt > interval * 3600 { try? await refresh(p.id) }
        }
        return activeServers != before
    }

    /// Servers of the active subscription, to see whether a refresh changed them.
    var activeServers: [String] { state.active.map { [$0.id] + $0.nodes.map(\.id) } ?? [] }

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
        Reminders.shared.sync()
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
    func setClipboardCheck(_ on: Bool) { update { $0.clipboardCheck = on } }
    func setReminders(_ on: Bool) {
        update { $0.reminders = on }
        Reminders.shared.sync()
    }

    /// Settings back to defaults; subscriptions, servers and favorites stay.
    func resetSettings() {
        update { st in
            st.settings = VelaSettings()
            st.theme = "system"
            st.sub = SubPrefs()
            st.connectOnLaunch = false
            st.alwaysOn = false
            st.clipboardCheck = true
            st.reminders = true
        }
        applyFetchOptions()
        Reminders.shared.sync()
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
        if let importError { return L("Настройки восстановлены. Подписки не загрузились:") + " " + importError }
        return msg.isEmpty ? L("Настройки восстановлены") : L("Восстановлено.") + " " + msg
    }

    // MARK: - Server check

    /// Set by «Остановить»: failures that arrive after it are not real.
    private nonisolated let stopFlag = StopFlag()
    nonisolated var stopRequested: Bool {
        get { stopFlag.value }
        set { stopFlag.value = newValue }
    }

    /// Real request through every server using a temporary core in the app.
    /// Call while the VPN is off; reasons for failures are reported.
    func probeOffline(_ profile: Profile) async {
        guard probe == nil else { return }
        beginProbe(profile.nodes)
        await streaming { feed in await self.probeWithTempCore(profile.nodes, feed: feed) }
        endProbe(profile.nodes)
    }

    /// Marks the servers as being checked (one update for the whole list).
    func beginProbe(_ nodes: [Node]) {
        stopRequested = false
        var p = pings, r = reasons
        for n in nodes {
            p[n.id] = 0
            r[n.id] = nil
        }
        pings = p
        reasons = r
        probe = (0, nodes.count, 0)
    }

    /// Ends a check: servers it didn't reach (stopped) don't stay "measuring".
    func endProbe(_ nodes: [Node]) {
        var p = pings
        for n in nodes where p[n.id] == 0 { p[n.id] = nil }
        pings = p
        probe = nil
        savePings()
    }

    /// Runs a check whose results go through a feed and reach the screen in
    /// batches, four times a second, as soon as they are known.
    func streaming(_ work: (ProbeFeed) async -> Void) async {
        let feed = ProbeFeed()
        let flusher = Task { @MainActor in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 250_000_000)
                Store.shared.show(feed.take())
            }
        }
        await work(feed)
        flusher.cancel()
        show(feed.take())
    }

    /// Puts a batch of check results on screen with one update.
    private func show(_ batch: [ProbeFeed.Item]) {
        guard !batch.isEmpty else { return }
        var p = pings, r = reasons
        var done = 0, ok = 0
        for x in batch {
            if x.ms <= 0, stopRequested {
                // the stop itself, not the server
                if p[x.id] == 0 { p[x.id] = nil }
                continue
            }
            p[x.id] = x.ms > 0 ? x.ms : -1
            if x.ms <= 0, !x.reason.isEmpty { r[x.id] = x.reason }
            done += 1
            if x.ms > 0 { ok += 1 }
        }
        pings = p
        reasons = r
        if let pr = probe, done > 0 { probe = (min(pr.total, pr.done + done), pr.total, pr.ok + ok) }
    }

    /// Real request through each server with a temporary core in the app;
    /// results go to the feed.
    func probeWithTempCore(_ nodes: [Node], feed: ProbeFeed) async {
        guard !nodes.isEmpty else { return }
        let nodesJSON = "[" + nodes.map(\.raw).joined(separator: ",") + "]"
        let callback = ProbeCallback { id, ms, reason in feed.add(id, ms, reason) }
        let failed: Bool = await Task.detached(priority: .userInitiated) {
            var err: NSError?
            let ok = VelacoreProbeNodes(nodesJSON, 9000, 12, callback, &err)
            return !ok || err != nil
        }.value
        guard failed, !stopRequested else { return }
        // temporary core unavailable: TCP reachability of the servers it
        // didn't report, many at once
        show(feed.take())
        let left = nodes.filter { (pings[$0.id] ?? 0) == 0 }
        let stop = stopFlag
        let reason = L("сервер недоступен")
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            let queue = OperationQueue()
            queue.maxConcurrentOperationCount = 16
            for n in left {
                queue.addOperation {
                    guard !stop.value else { return }
                    let ms = Int(VelacoreTCPPing(n.server, Int32(n.port)))
                    feed.add(n.id, ms > 0 ? ms : -1, ms > 0 ? "" : reason)
                }
            }
            queue.addBarrierBlock {
                _ = queue
                done.resume()
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
        let failure: String? = await Task.detached(priority: .userInitiated) {
            var err: NSError?
            let ok = VelacoreProbeNodes(json, 9000, 1, callback, &err)
            return ok && err == nil ? nil : (err?.localizedDescription ?? "")
        }.value
        if let (ms, reason) = box.get() {
            pings[node.id] = ms > 0 ? ms : -1
            if ms <= 0, !reason.isEmpty { reasons[node.id] = reason }
        } else if let failure, !stopRequested {
            // the temporary core didn't start: at least whether it's reachable
            let ms = await Task.detached { Int(VelacoreTCPPing(node.server, Int32(node.port))) }.value
            pings[node.id] = ms > 0 ? ms : -1
            if ms <= 0 { reasons[node.id] = failure.isEmpty ? L("сервер недоступен") : failure }
        } else {
            pings[node.id] = nil // stopped
            return
        }
        savePings()
    }

    // MARK: - Config

    /// Builds the sing-box config. Paths point to a placeholder that the
    /// Network Extension replaces with its own data directory, so the app and
    /// the extension don't need a shared container.
    func buildConfig(port: Int, secret: String) async throws -> String {
        try await Self.build(configRequest(port: port, secret: secret))
    }

    /// The request for the core's config builder, from the current state.
    /// Quick (runs on the main thread): the servers' JSON is passed as it is
    /// stored, not parsed and written again.
    func configRequest(port: Int, secret: String) throws -> String {
        guard let profile = state.active, !profile.nodes.isEmpty else {
            throw CoreError(message: L("Сначала добавьте сервер или подписку"))
        }
        let ids = Set(profile.nodes.map(\.id))
        // automatic selection starts with the first of these (until its own
        // test answers): favorites and the fastest by the last check first,
        // never the dead ones
        let ranked = pings.filter { ids.contains($0.key) && $0.value > 0 }.sorted { $0.value < $1.value }.map(\.key)
        let failed = pings.filter { ids.contains($0.key) && $0.value < 0 }.map(\.key)
        let order = Dictionary(ranked.enumerated().map { ($1, $0) }, uniquingKeysWith: { a, _ in a })
        // working favorites by the last check (unchecked ones after them),
        // not in the order they were starred
        let favorites = state.favorites.filter { ids.contains($0) }.enumerated()
            .sorted { (order[$0.element] ?? Int.max, $0.offset) < (order[$1.element] ?? Int.max, $1.offset) }
            .map(\.element)
        let selected = profile.id == state.activeProfile ? state.selectedNode : "auto"
        var list = profile.nodes
        if state.settings.routing == "blocked", list.count > Self.blockedModeServers {
            list = Self.shortList(list, selected: selected, favorites: favorites, ranked: ranked, failed: Set(failed))
        }
        let req: [String: Any] = [
            "platform": "ios",
            "selected": selected,
            "settings": state.settings.json, "dataDir": "__VELA_DATA__", "skipRuleInstall": true,
            "controllerPort": port, "controllerSecret": secret,
            "favorites": favorites, "ranked": ranked, "failed": failed,
            "logPath": "__VELA_DATA__/core.log",
        ]
        let data = try JSONSerialization.data(withJSONObject: req)
        guard let text = String(data: data, encoding: .utf8), text.hasPrefix("{"), text.count > 2 else {
            throw CoreError(message: LF("Конфигурация не собирается: %@", "JSON"))
        }
        // each node's raw JSON is an object written by JSONSerialization
        let raws = list.map(\.raw).filter { $0.hasPrefix("{") && $0.hasSuffix("}") }
        return "{\"nodes\":[" + raws.joined(separator: ",") + "]," + String(text.dropFirst())
    }

    /// Runs the core's config builder off the main thread.
    nonisolated static func build(_ request: String) async throws -> String {
        try await Task.detached(priority: .userInitiated) {
            var error: NSError?
            let config = VelacoreBuildConfig(request, &error)
            if let error { throw CoreError(message: error.localizedDescription) }
            return config
        }.value
    }

    /// «Только заблокированное» loads big lists of sites and addresses into
    /// the ~50 MB of the Network Extension, so the core gets fewer servers
    /// then (it keeps up to 30 for automatic selection plus the favorites).
    static let blockedModeServers = 20

    /// The chosen server, working favorites, the fastest, then unchecked ones.
    static func shortList(_ nodes: [Node], selected: String, favorites: [String], ranked: [String], failed: Set<String>) -> [Node] {
        let byID = Dictionary(nodes.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        var out: [Node] = []
        var taken = Set<String>()
        func take(_ id: String) {
            guard out.count < blockedModeServers, !taken.contains(id), let n = byID[id] else { return }
            taken.insert(id)
            out.append(n)
        }
        take(selected)
        for id in favorites where !failed.contains(id) { take(id) }
        for id in ranked { take(id) }
        for n in nodes where !failed.contains(n.id) { take(n.id) }
        for n in nodes { take(n.id) }
        return out
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
        p.announce = (sub["announce"] as? String).flatMap { $0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : $0 }
        let support = (sub["supportUrl"] as? String) ?? ""
        p.supportURL = support.isEmpty ? ((sub["webPageUrl"] as? String) ?? "") : support
        return p
    }

    /// Keeps profile names unique so tabs can be told apart.
    nonisolated static func uniqueName(_ all: [Profile], _ p: Profile) -> String {
        if let custom = p.customName, !custom.isEmpty { return custom }
        let base = p.name.isEmpty ? (URL(string: p.url)?.host ?? L("Подписка")) : p.name
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
