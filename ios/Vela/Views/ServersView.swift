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
    @State private var fastestFirst = false

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
                        Text("Проверяем серверы: \(probe.done) из \(probe.total)").font(.footnote).foregroundStyle(Palette.muted)
                        ProgressView(value: Double(probe.done), total: Double(max(1, probe.total))).tint(Palette.star)
                    }
                    .listRowBackground(Color.clear)
                }
                if let p = profile {
                    if p.kind == "subscription" {
                        subscriptionCard(p).listRowBackground(Color.clear).listRowInsets(EdgeInsets(top: 4, leading: 16, bottom: 4, trailing: 16))
                    }
                    Toggle("Сначала быстрые", isOn: $fastestFirst)
                        .toggleStyle(.button).font(.footnote).tint(Palette.star)
                        .listRowBackground(Color.clear)
                    rows(p)
                } else {
                    empty.listRowBackground(Color.clear)
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .background(Palette.ink)
            .refreshable { await refresh() }
            .navigationTitle("Серверы")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Готово") { dismiss() } }
                ToolbarItemGroup(placement: .primaryAction) {
                    Button { Task { await check() } } label: { Image(systemName: "speedometer") }
                        .disabled(store.probe != nil)
                        .accessibilityLabel("Проверить все серверы")
                    Button("Добавить") { showAdd = true }
                }
            }
            .alert(confirmDelete?.kind == "manual" ? "Удалить серверы?" : "Удалить подписку?", isPresented: Binding(get: { confirmDelete != nil }, set: { if !$0 { confirmDelete = nil } })) {
                Button("Удалить", role: .destructive) {
                    if let p = confirmDelete {
                        if p.id == store.state.activeProfile, tunnel.state != .disconnected { tunnel.disconnect() }
                        store.deleteProfile(p.id)
                    }
                }
                Button("Отмена", role: .cancel) {}
            } message: {
                Text("«\(confirmDelete?.name ?? "")» и все её серверы будут удалены с этого устройства.")
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
    }

    private var tabs: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                ForEach(store.state.profiles) { p in
                    let sel = p.id == profile?.id
                    Button((p.id == store.state.activeProfile ? "● " : "") + p.name + "  \(p.nodes.count)") { viewID = p.id }
                        .font(.system(size: 14))
                        .foregroundStyle(sel ? Palette.text : Palette.muted)
                        .padding(.horizontal, 14).padding(.vertical, 7)
                        .background(sel ? Palette.hull : .clear, in: Capsule())
                        .overlay(Capsule().stroke(sel ? Palette.line : Palette.hull))
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
            if p.total > 0 {
                info("Трафик", "\(Fmt.bytes(used)) из \(Fmt.bytes(p.total))")
                let frac = min(1, Double(used) / Double(p.total))
                ProgressView(value: frac).tint(frac > 0.85 ? Palette.flare : Palette.star)
            } else if used > 0 {
                info("Трафик", "\(Fmt.bytes(used)), без лимита")
            }
            if p.expire > 0 {
                let days = Int((Double(p.expire) - Date().timeIntervalSince1970) / 86400)
                info("Действует до", Fmt.date(p.expire) + ((0 ... 7).contains(days) ? " (осталось дней: \(days))" : ""))
            }
            HStack(spacing: 14) {
                Button(refreshing ? "Обновляем…" : "Обновить") { Task { await refresh() } }.disabled(refreshing)
                if store.state.profiles.filter({ $0.kind == "subscription" }).count > 1 {
                    Button("Все") { Task { await refreshAll() } }.disabled(refreshing)
                }
                Button("Имя") { renameText = p.customName ?? p.name; renaming = p }
                if let url = URL(string: p.supportURL), !p.supportURL.isEmpty {
                    Button("Поддержка") { openURL(url) }
                }
                Spacer()
                Button("Удалить") { confirmDelete = p }
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

    private func info(_ label: String, _ value: String) -> some View {
        HStack {
            Text(label).foregroundStyle(Palette.muted)
            Spacer()
            Text(value).fontWeight(.semibold)
        }
        .font(.system(size: 13))
    }

    @ViewBuilder private func rows(_ p: Profile) -> some View {
        let isActive = p.id == store.state.activeProfile
        let sel = store.state.selectedNode
        let autoSelected = isActive && (sel == "auto" || !p.nodes.contains { $0.id == sel })
        let connected = tunnel.state == .connected && isActive
        let live = autoSelected && connected ? p.nodes.first { $0.id == tunnel.activeNode } : nil

        row(badge: "✦", title: "Автовыбор",
            meta: live.map { "Сейчас: " + Fmt.splitFlag($0.name).rest } ?? "Самый быстрый сервер, переключается при сбое",
            ping: nil, selected: autoSelected, inUse: false, reason: nil) { choose(p, "auto") }

        let ordered = fastestFirst ? p.nodes.sorted { rank(store.pings[$0.id]) < rank(store.pings[$1.id]) } : p.nodes
        ForEach(ordered) { n in
            let tags = [n.proto, n.security, (!n.network.isEmpty && n.network != "tcp") ? n.network.uppercased() : ""]
                .filter { !$0.isEmpty }.joined(separator: " ")
            row(badge: Fmt.badge(n.name), title: Fmt.splitFlag(n.name).rest, meta: tags,
                ping: Fmt.ping(store.pings[n.id]), selected: isActive && sel == n.id,
                inUse: connected && tunnel.activeNode == n.id,
                reason: (store.pings[n.id] ?? 0) < 0 ? store.reasons[n.id] : nil) { choose(p, n.id) }
                .swipeActions {
                    if p.kind == "manual" {
                        Button("Удалить", role: .destructive) { store.deleteNode(profile: p.id, node: n.id) }
                    }
                }
        }
    }

    private func rank(_ ms: Int?) -> Int {
        guard let ms else { return 1_000_000 }
        if ms > 0 { return ms }
        return ms == 0 ? 1_000_000 : 10_000_000
    }

    private func check() async {
        guard let p = profile else { return }
        await tunnel.pingAll(p.id)
        let ok = p.nodes.filter { (store.pings[$0.id] ?? 0) > 0 }.count
        message = "Работают \(ok) из \(p.nodes.count)"
    }

    private func refreshAll() async {
        refreshing = true
        defer { refreshing = false }
        do { message = try await store.refreshAll() } catch { message = error.localizedDescription }
    }

    private func row(badge: String, title: String, meta: String, ping: (String, Color)?, selected: Bool, inUse: Bool, reason: String?, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Badge(text: badge, size: 36)
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).fontWeight(.semibold).lineLimit(1)
                    HStack(spacing: 6) {
                        Text(meta).font(.caption).foregroundStyle(Palette.muted).lineLimit(1)
                        if inUse { Text("● используется").font(.caption).foregroundStyle(Palette.sea) }
                    }
                    if let reason, !reason.isEmpty {
                        Text(reason).font(.caption).foregroundStyle(Palette.danger).lineLimit(2)
                    }
                }
                Spacer()
                if let ping { Text(ping.0).font(.system(size: 13)).foregroundStyle(ping.1).monospacedDigit() }
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
        if tunnel.state == .connected {
            Task {
                var switched = false
                if sameProfile { switched = await tunnel.switchTo(node) }
                if !switched { await tunnel.reload() }
            }
        }
        dismiss()
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
