import SwiftUI

struct ServersView: View {
    @EnvironmentObject var store: Store
    @EnvironmentObject var tunnel: TunnelManager
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @Binding var showAdd: Bool
    @State private var viewID = ""
    @State private var refreshing = false
    @State private var confirmDelete: Profile?
    @State private var message: String?
    @State private var renaming: Profile?
    @State private var renameText = ""
    private var fastestFirst: Bool { store.state.sortByPing }
    @State private var query = ""
    @State private var onlyFav = false
    @State private var onlyOK = false
    @State private var proto = ""
    @State private var country = ""
    @State private var sharing: ShareItem?

    /// The search words, prepared once per redraw (not once per server).
    private var words: [Substring] {
        query.trimmingCharacters(in: .whitespaces).lowercased().split(separator: " ")
    }

    private func matches(_ n: Node, _ words: [Substring]) -> Bool {
        if !words.isEmpty {
            let flag = Fmt.splitFlag(n.name).flag
            let hay = [n.name, n.proto, n.security, n.network, n.server, flag.map(Fmt.country) ?? ""].joined(separator: " ").lowercased()
            if words.contains(where: { !hay.contains($0) }) { return false }
        }
        if onlyFav, !store.state.favorites.contains(n.id) { return false }
        if onlyOK, (store.pings[n.id] ?? 0) <= 0 { return false }
        if !proto.isEmpty, n.proto != proto { return false }
        if !country.isEmpty, Fmt.splitFlag(n.name).flag != country { return false }
        return true
    }

    private var profile: Profile? {
        store.state.profiles.first { $0.id == viewID } ?? store.state.active
    }

    var body: some View {
        NavigationStack {
            List {
                if !store.state.profiles.isEmpty {
                    tabs.listRowBackground(Color.clear).listRowInsets(EdgeInsets())
                }
                if let probe = store.probe {
                    VStack(alignment: .leading, spacing: 6) {
                        HStack {
                            Text(LF("Проверено %@ из %@ · работают %@", "\(probe.done)", "\(probe.total)", "\(probe.ok)")).font(.footnote).foregroundStyle(Palette.muted)
                            Spacer()
                            Button("Остановить") { tunnel.stopProbe() }.font(.footnote).buttonStyle(.borderless)
                        }
                        ProgressView(value: Double(probe.done), total: Double(max(1, probe.total))).tint(Palette.star)
                    }
                    .listRowBackground(Color.clear)
                }
                if let p = profile {
                    if p.kind == "subscription" {
                        subscriptionCard(p).listRowBackground(Color.clear).listRowInsets(EdgeInsets(top: 4, leading: 16, bottom: 4, trailing: 16))
                    }
                    filters(p).listRowBackground(Color.clear).listRowInsets(EdgeInsets(top: 2, leading: 0, bottom: 2, trailing: 0))
                    rows(p)
                } else {
                    empty.listRowBackground(Color.clear)
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .background(Palette.ink)
            .refreshable { await refresh() }
            .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Страна, город, протокол")
            .sheet(item: $sharing) { item in ShareSheet(title: item.title, hint: item.hint, text: item.text) }
            .navigationTitle("Серверы")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Готово") { dismiss() } }
                ToolbarItemGroup(placement: .primaryAction) {
                    if store.state.profiles.contains(where: { $0.kind == "subscription" }) {
                        Button { Task { await refreshCurrent() } } label: {
                            if refreshing { ProgressView() } else { Image(systemName: "arrow.clockwise") }
                        }
                        .disabled(refreshing)
                        .accessibilityLabel("Обновить подписку")
                    }
                    if store.probe != nil {
                        Button { tunnel.stopProbe() } label: { Image(systemName: "stop.circle.fill").foregroundStyle(Palette.danger) }
                            .accessibilityLabel("Остановить проверку")
                    } else {
                        Button { Task { await check() } } label: { Image(systemName: "speedometer") }
                            .accessibilityLabel("Проверить все серверы")
                    }
                    Button("Добавить") { showAdd = true }
                }
            }
            .alert(confirmDelete?.kind == "manual" ? L("Удалить серверы?") : L("Удалить подписку?"), isPresented: Binding(get: { confirmDelete != nil }, set: { if !$0 { confirmDelete = nil } })) {
                Button("Удалить", role: .destructive) {
                    if let p = confirmDelete {
                        if p.id == store.state.activeProfile, tunnel.state != .disconnected { tunnel.disconnect() }
                        store.deleteProfile(p.id)
                    }
                }
                Button("Отмена", role: .cancel) {}
            } message: {
                Text(confirmDelete?.kind == "manual" ? L("Все ваши серверы будут удалены с этого устройства.")
                    : LF("«%@» и все её серверы будут удалены с этого устройства.", confirmDelete?.displayName ?? ""))
            }
            .alert("Название подписки", isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
                TextField("Название", text: $renameText)
                Button("Сохранить") {
                    if let p = renaming { store.rename(p.id, to: renameText) }
                }
                Button("Отмена", role: .cancel) {}
            } message: {
                Text("Пустое поле вернёт название от провайдера.")
            }
            .alert(message ?? "", isPresented: Binding(get: { message != nil }, set: { if !$0 { message = nil } })) {
                Button("OK", role: .cancel) {}
            }
        }
        .tint(Palette.star)
        .onAppear { viewID = store.state.active?.id ?? "" }
        // another subscription may have no such country or protocol
        .onChange(of: viewID) { _ in proto = ""; country = "" }
    }

    private var tabs: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                ForEach(store.state.profiles) { p in
                    let sel = p.id == profile?.id
                    Button((p.id == store.state.activeProfile ? "● " : "") + p.displayName + "  \(p.nodes.count)") { viewID = p.id }
                        .font(.system(size: 14))
                        .foregroundStyle(sel ? Palette.text : Palette.muted)
                        .padding(.horizontal, 14).padding(.vertical, 7)
                        .background(sel ? Palette.hull : .clear, in: Capsule())
                        .overlay(Capsule().stroke(sel ? Palette.line : Palette.hull))
                        .accessibilityLabel(p.displayName + ", " + LF("Серверов: %@", "\(p.nodes.count)"))
                        .accessibilityAddTraits(sel ? .isSelected : [])
                }
                Button("+ Подписка") { showAdd = true }
                    .font(.system(size: 14))
                    .foregroundStyle(Palette.star)
                    .padding(.horizontal, 14).padding(.vertical, 7)
                    .overlay(Capsule().stroke(Palette.line, style: StrokeStyle(lineWidth: 1, dash: [4, 3])))
            }
            .padding(.horizontal, 16).padding(.vertical, 8)
        }
    }

    private func subscriptionCard(_ p: Profile) -> some View {
        let used = p.upload + p.download
        return VStack(alignment: .leading, spacing: 6) {
            if let a = p.announce, !a.isEmpty {
                HStack(alignment: .top, spacing: 8) {
                    Image(systemName: "megaphone").foregroundStyle(Palette.flare).font(.system(size: 13))
                    Text(a).font(.system(size: 13)).fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
                }
                .padding(.bottom, 2)
            }
            if p.total > 0 {
                info(L("Трафик"), LF("%@ из %@", Fmt.bytes(used), Fmt.bytes(p.total)))
                let frac = min(1, Double(used) / Double(p.total))
                ProgressView(value: frac).tint(frac >= 0.9 ? Palette.danger : (frac > 0.85 ? Palette.flare : Palette.star))
            } else if used > 0 {
                info(L("Трафик"), LF("%@, без лимита", Fmt.bytes(used)))
            }
            if p.expire > 0 {
                let left = Double(p.expire) - Date().timeIntervalSince1970
                let days = Int(left / 86400)
                if left <= 0 {
                    info(L("Закончилась"), Fmt.date(p.expire), color: Palette.danger)
                } else {
                    info(L("Действует до"), Fmt.date(p.expire) + ((0 ... 7).contains(days) ? ", " + LF("осталось дней: %@", "\(days)") : ""),
                         color: days < 3 ? Palette.flare : nil)
                }
            }
            // all buttons in a row when they fit; on narrow screens (iPhone SE,
            // large text) the rarer ones go into a menu instead of being cut off
            ViewThatFits(in: .horizontal) {
                cardActions(p, compact: false)
                cardActions(p, compact: true)
            }
            .buttonStyle(.borderless)
            .font(.system(size: 14))
            .foregroundStyle(Palette.muted)
            .padding(.top, 4)
        }
        .padding(14)
        .background(Palette.deep, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).stroke(Palette.hull))
    }

    private func cardActions(_ p: Profile, compact: Bool) -> some View {
        let support = URL(string: p.supportURL).flatMap { p.supportURL.isEmpty ? nil : $0 }
        return HStack(spacing: 14) {
            Button(refreshing ? L("Обновляем…") : L("Обновить")) { Task { await refresh() } }.disabled(refreshing)
            if store.state.profiles.filter({ $0.kind == "subscription" }).count > 1 {
                Button("Все") { Task { await refreshAll() } }.disabled(refreshing)
            }
            Button("QR") { share(p.name, p.url) }
            if compact {
                Spacer(minLength: 0)
                Menu {
                    Button { renameText = p.customName ?? p.name; renaming = p } label: { Label("Имя", systemImage: "pencil") }
                    if let support {
                        Button { openURL(support) } label: { Label("Поддержка", systemImage: "questionmark.circle") }
                    }
                    Button(role: .destructive) { confirmDelete = p } label: { Label("Удалить", systemImage: "trash") }
                } label: {
                    Image(systemName: "ellipsis.circle").font(.system(size: 17)).frame(width: 32, height: 28)
                }
                .accessibilityLabel(L("Ещё"))
            } else {
                Button("Имя") { renameText = p.customName ?? p.name; renaming = p }
                if let support { Button("Поддержка") { openURL(support) } }
                Spacer(minLength: 8)
                Button("Удалить") { confirmDelete = p }
            }
        }
        .lineLimit(1)
    }

    private func info(_ label: String, _ value: String, color: Color? = nil) -> some View {
        HStack {
            Text(label).foregroundStyle(Palette.muted)
            Spacer()
            Text(value).fontWeight(.semibold).foregroundStyle(color ?? Palette.text)
        }
        .font(.system(size: 13))
    }

    private func share(_ title: String, _ text: String) {
        sharing = ShareItem(title: title, hint: L("Отсканируйте QR-код в Vela или другом клиенте на другом устройстве."), text: text)
    }

    private func filters(_ p: Profile) -> some View {
        // one pass: this runs on every redraw (results of a check, the
        // traffic numbers), and per-country counting was slow with 500+ servers
        var protoSet = Set<String>()
        var perFlag: [String: Int] = [:]
        for n in p.nodes {
            if !n.proto.isEmpty { protoSet.insert(n.proto) }
            if let f = Fmt.splitFlag(n.name).flag { perFlag[f, default: 0] += 1 }
        }
        let protocols = protoSet.sorted()
        let flags = perFlag.keys.sorted { Fmt.country($0) < Fmt.country($1) }
        return ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                Chip(text: L("★ Избранные"), on: onlyFav) { onlyFav.toggle() }
                Chip(text: L("Рабочие"), on: onlyOK) { onlyOK.toggle() }
                if protocols.count > 1 || !proto.isEmpty {
                    Menu {
                        Button("Все протоколы") { proto = "" }
                        ForEach(protocols, id: \.self) { v in Button(v) { proto = v } }
                    } label: {
                        Chip(text: (proto.isEmpty ? L("Все протоколы") : proto) + " ▾", on: !proto.isEmpty) {}.allowsHitTesting(false)
                    }
                }
                if flags.count > 1 || !country.isEmpty {
                    Menu {
                        Button("Все страны") { country = "" }
                        ForEach(flags, id: \.self) { f in
                            Button("\(f) \(Fmt.country(f))  \(perFlag[f] ?? 0)") { country = f }
                        }
                    } label: {
                        Chip(text: (country.isEmpty ? L("Все страны") : country + " " + Fmt.country(country)) + " ▾", on: !country.isEmpty) {}.allowsHitTesting(false)
                    }
                }
                Chip(text: L("Сначала быстрые"), on: fastestFirst) { store.setSortByPing(!fastestFirst) }
            }
            .padding(.horizontal, 16).padding(.vertical, 4)
        }
    }

    @ViewBuilder private func rows(_ p: Profile) -> some View {
        let isActive = p.id == store.state.activeProfile
        let sel = store.state.selectedNode
        let autoSelected = isActive && (sel == "auto" || !p.nodes.contains { $0.id == sel })
        let connected = tunnel.state == .connected && isActive
        let live = autoSelected && connected ? p.nodes.first { $0.id == tunnel.activeNode } : nil

        let favs = Set(store.state.favorites)
        let hasFavs = p.nodes.contains { favs.contains($0.id) }
        row(badge: "✦", title: L("Автовыбор"),
            meta: live.map { LF("Сейчас: %@", Fmt.splitFlag($0.name).rest) }
                ?? (hasFavs ? L("Среди избранных, переключается при сбое") : L("Самый быстрый сервер, переключается при сбое")),
            ping: nil, selected: autoSelected, inUse: false, reason: nil) { choose(p, "auto") }

        let w = words
        let visible = p.nodes.filter { matches($0, w) }
        let ordered = fastestFirst
            ? visible.sorted { rank(store.pings[$0.id]) < rank(store.pings[$1.id]) }
            : visible.filter { favs.contains($0.id) } + visible.filter { !favs.contains($0.id) }
        if visible.count != p.nodes.count {
            Text(LF("Показано %@ из %@", "\(visible.count)", "\(p.nodes.count)")).font(.caption).foregroundStyle(Palette.muted).listRowBackground(Color.clear)
        }
        if visible.isEmpty {
            let elsewhere = query.isEmpty ? [] : store.state.profiles.filter { $0.id != p.id && $0.nodes.contains(where: { matches($0, w) }) }
            VStack(spacing: 6) {
                Text("Ничего не найдено").font(.headline)
                Text(elsewhere.isEmpty ? L("Измените поиск или фильтры.") : L("Есть совпадения в других подписках:"))
                    .foregroundStyle(Palette.muted)
                ForEach(elsewhere) { o in
                    Button(o.displayName + " · \(o.nodes.filter({ matches($0, w) }).count)") { viewID = o.id }
                }
            }
            .frame(maxWidth: .infinity).padding(20)
            .listRowBackground(Color.clear)
        }
        ForEach(ordered) { n in
            let tags = [n.proto, n.security, (!n.network.isEmpty && n.network != "tcp") ? n.network.uppercased() : ""]
                .filter { !$0.isEmpty }.joined(separator: " ")
            row(badge: Fmt.badge(n.name), title: Fmt.splitFlag(n.name).rest, meta: tags, xray: n.isXray,
                ping: Fmt.ping(store.pings[n.id]), selected: isActive && sel == n.id,
                inUse: connected && tunnel.activeNode == n.id,
                reason: (store.pings[n.id] ?? 0) < 0 ? store.reasons[n.id] : nil,
                favorite: favs.contains(n.id), onFavorite: { store.setFavorite(n.id, !favs.contains(n.id)) },
                onPing: { Task { await tunnel.pingOne(profile: p.id, node: n.id) } }) { choose(p, n.id) }
                .swipeActions {
                    if p.kind == "manual" {
                        Button("Удалить", role: .destructive) {
                            store.deleteNode(profile: p.id, node: n.id)
                            // the running core must not keep a deleted server
                            if p.id == store.state.activeProfile { Task { await tunnel.reload() } }
                        }
                    }
                    Button { share(Fmt.splitFlag(n.name).rest, n.link) } label: { Label("QR", systemImage: "qrcode") }.tint(Palette.hull)
                }
                .contextMenu {
                    Button { store.setFavorite(n.id, !favs.contains(n.id)) } label: {
                        Label(favs.contains(n.id) ? L("Убрать из избранного") : L("В избранное"), systemImage: favs.contains(n.id) ? "star.slash" : "star")
                    }
                    Button { share(Fmt.splitFlag(n.name).rest, n.link) } label: { Label("Поделиться", systemImage: "qrcode") }
                    Button { Task { await tunnel.pingOne(profile: p.id, node: n.id) } } label: { Label("Проверить сервер", systemImage: "speedometer") }
                }
        }
    }

    private func rank(_ ms: Int?) -> Int {
        guard let ms else { return 1_000_000 }
        if ms > 0 { return ms }
        return ms == 0 ? 1_000_000 : 10_000_000
    }

    private func check() async {
        guard let p = profile, !p.nodes.isEmpty else { return }
        // the running core can't check another subscription (the error would
        // land on the home screen behind this sheet)
        if tunnel.state != .disconnected, p.id != store.state.activeProfile {
            message = L("Чтобы проверить другую подписку, отключитесь")
            return
        }
        await tunnel.pingAll(p.id)
        // stopped: the list already shows what was checked
        guard !store.stopRequested else { return }
        let ok = p.nodes.filter { (store.pings[$0.id] ?? 0) > 0 }.count
        message = LF("Работают %@ из %@", "\(ok)", "\(p.nodes.count)")
    }

    private func refreshAll() async {
        refreshing = true
        defer { refreshing = false }
        do { message = try await store.refreshAll() } catch { message = error.localizedDescription }
    }

    private func row(badge: String, title: String, meta: String, xray: Bool = false, ping: (String, Color)?, selected: Bool, inUse: Bool, reason: String?,
                     favorite: Bool = false, onFavorite: (() -> Void)? = nil, onPing: (() -> Void)? = nil,
                     action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Badge(text: badge, size: 36)
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).fontWeight(.semibold).lineLimit(1)
                    HStack(spacing: 6) {
                        if xray { MiniTag(text: "Xray") }
                        Text(meta).font(.caption).foregroundStyle(Palette.muted).lineLimit(1)
                        if inUse { Text("● используется").font(.caption).foregroundStyle(Palette.sea) }
                    }
                    if let reason, !reason.isEmpty {
                        Text(reason).font(.caption).foregroundStyle(Palette.danger).lineLimit(2)
                    }
                }
                Spacer()
                // tap the delay (or the gauge) to check only this server
                if let onPing {
                    Button(action: onPing) {
                        if let ping {
                            Text(ping.0).font(.system(size: 13)).foregroundStyle(ping.1).monospacedDigit()
                        } else {
                            Image(systemName: "speedometer").foregroundStyle(Palette.line)
                        }
                    }
                    .buttonStyle(.borderless)
                    .frame(minWidth: 32, minHeight: 32)
                    .accessibilityLabel("Проверить сервер")
                } else if let ping {
                    Text(ping.0).font(.system(size: 13)).foregroundStyle(ping.1).monospacedDigit()
                }
                if let onFavorite {
                    Button(action: onFavorite) {
                        Image(systemName: favorite ? "star.fill" : "star")
                            .foregroundStyle(favorite ? Palette.flare : Palette.line)
                            .frame(width: 32, height: 32)
                    }
                    .buttonStyle(.borderless)
                    .accessibilityLabel(favorite ? L("Убрать из избранного") : L("В избранное"))
                }
                if selected { Image(systemName: "checkmark").foregroundStyle(Palette.sea).fontWeight(.semibold) }
            }
            .padding(.vertical, 4)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .listRowBackground(selected ? Palette.deep : Palette.ink)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    private var empty: some View {
        VStack(spacing: 8) {
            Text("Серверов пока нет").font(.headline)
            Text("Вставьте ссылку на подписку от вашего провайдера или ссылку на сервер.")
                .foregroundStyle(Palette.muted).multilineTextAlignment(.center)
            Button("Добавить серверы") { showAdd = true }.buttonStyle(.borderedProminent).foregroundStyle(Palette.onStar).padding(.top, 8)
        }
        .frame(maxWidth: .infinity).padding(24)
    }

    private func choose(_ p: Profile, _ node: String) {
        let sameProfile = p.id == store.state.activeProfile
        store.select(profile: p.id, node: node)
        Task {
            var switched = false
            if sameProfile, tunnel.state == .connected { switched = await tunnel.switchTo(node) }
            // while connecting (or reconnected by iOS later) it's sent once the tunnel is up
            if !switched { await tunnel.reload() }
        }
        dismiss()
    }

    /// The refresh button: the subscription on screen, or all of them.
    private func refreshCurrent() async {
        if let p = profile, p.kind == "subscription" {
            await refresh()
        } else {
            await refreshAll()
        }
    }

    private func refresh() async {
        guard let p = profile, p.kind == "subscription" else { return }
        refreshing = true
        defer { refreshing = false }
        do {
            try await store.refresh(p.id)
            if p.id == store.state.activeProfile { await tunnel.reload() }
        } catch {
            message = error.localizedDescription
        }
    }
}
