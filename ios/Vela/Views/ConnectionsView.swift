import SwiftUI

/// Open connections of the running core, refreshed while on screen.
struct ConnectionsView: View {
    @EnvironmentObject var store: Store
    @EnvironmentObject var tunnel: TunnelManager
    @Environment(\.dismiss) private var dismiss
    @State private var list: [Conn] = []
    @State private var error: String?
    @State private var loaded = false
    @State private var query = ""
    @State private var confirmAll = false
    @State private var now = Date()

    /// Server names of the active subscription by node id.
    private var names: [String: String] {
        let nodes = store.state.active?.nodes ?? []
        return Dictionary(nodes.map { ($0.id, Fmt.splitFlag($0.name).rest) }, uniquingKeysWith: { a, _ in a })
    }

    private var filtered: [Conn] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        guard !q.isEmpty else { return list }
        let names = self.names
        return list.filter { c in
            let hay = [c.host, c.destination, c.rule, c.network, c.process, route(c, names)].joined(separator: " ").lowercased()
            return q.split(separator: " ").allSatisfy { hay.contains($0) }
        }
    }

    var body: some View {
        NavigationStack {
            List {
                if tunnel.state != .connected {
                    placeholder(L("VPN не подключён"), L("Соединения видны, пока VPN включён."))
                } else if let error, list.isEmpty {
                    placeholder(L("Не удалось получить список"), error)
                } else if loaded, filtered.isEmpty {
                    placeholder(query.isEmpty ? L("Соединений нет") : L("Ничего не найдено"),
                                query.isEmpty ? L("Они появятся, когда приложения начнут что-то загружать.") : L("Измените поиск."))
                } else if !loaded {
                    ProgressView().frame(maxWidth: .infinity).padding(30).listRowBackground(Color.clear)
                }
                if tunnel.state == .connected, !filtered.isEmpty {
                    let names = self.names
                    Text(filtered.count == list.count ? LF("Открыто: %@", "\(list.count)") : LF("Показано %@ из %@", "\(filtered.count)", "\(list.count)"))
                        .font(.caption).foregroundStyle(Palette.muted)
                        .listRowBackground(Color.clear)
                    ForEach(filtered) { c in
                        row(c, names)
                            .swipeActions {
                                Button(role: .destructive) { close(c.id) } label: { Label("Закрыть", systemImage: "xmark") }
                            }
                            .contextMenu {
                                Button { Pickup.shared.copy(c.host.isEmpty ? c.destination : c.host) } label: {
                                    Label("Скопировать адрес", systemImage: "doc.on.doc")
                                }
                                Button(role: .destructive) { close(c.id) } label: { Label("Закрыть соединение", systemImage: "xmark") }
                            }
                    }
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .background(Palette.ink)
            .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Сайт, сервер или правило")
            .navigationTitle("Соединения")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Готово") { dismiss() } }
                ToolbarItem(placement: .primaryAction) {
                    Button("Закрыть все") { confirmAll = true }.disabled(list.isEmpty || tunnel.state != .connected)
                }
            }
            .confirmationDialog("Закрыть все соединения?", isPresented: $confirmAll, titleVisibility: .visible) {
                Button("Закрыть все", role: .destructive) { close("") }
            } message: {
                Text("Приложения откроют их заново, если они ещё нужны.")
            }
        }
        .tint(Palette.star)
        .task { await poll() }
    }

    private func placeholder(_ title: String, _ text: String) -> some View {
        VStack(spacing: 6) {
            Text(title).font(.headline)
            if !text.isEmpty { Text(text).foregroundStyle(Palette.muted).multilineTextAlignment(.center) }
        }
        .frame(maxWidth: .infinity)
        .padding(24)
        .listRowBackground(Color.clear)
    }

    private func row(_ c: Conn, _ names: [String: String]) -> some View {
        let direct = c.outbound == "direct" || c.outbound.isEmpty
        return VStack(alignment: .leading, spacing: 3) {
            HStack(alignment: .firstTextBaseline) {
                Text(c.host.isEmpty ? c.destination : c.host)
                    .font(.system(size: 15, weight: .semibold))
                    .lineLimit(1).truncationMode(.middle)
                Spacer(minLength: 8)
                Text(Fmt.age(Int(now.timeIntervalSince1970 - Double(c.start) / 1000)))
                    .font(.caption).foregroundStyle(Palette.muted).monospacedDigit()
            }
            HStack(spacing: 6) {
                Text(route(c, names)).foregroundStyle(direct ? Palette.muted : Palette.sea).lineLimit(1)
                if !c.network.isEmpty { Text(c.network.uppercased()).foregroundStyle(Palette.muted) }
                Spacer(minLength: 8)
                Text("↑ " + Fmt.bytes(c.upload) + "  ↓ " + Fmt.bytes(c.download))
                    .foregroundStyle(Palette.muted).monospacedDigit().lineLimit(1)
            }
            .font(.caption)
            if !c.rule.isEmpty {
                Text(c.rule).font(.caption2).foregroundStyle(Palette.muted).lineLimit(1).truncationMode(.tail)
            }
        }
        .padding(.vertical, 2)
        .listRowBackground(Palette.ink)
        .accessibilityElement(children: .combine)
    }

    /// Where the connection goes: the server's name, directly or blocked.
    private func route(_ c: Conn, _ names: [String: String]) -> String {
        let o = c.outbound
        if o.hasPrefix("node-") { return names[String(o.dropFirst(5))] ?? L("Сервер") }
        switch o {
        case "", "direct": return L("Напрямую")
        case "block", "reject": return L("Заблокировано")
        case "proxy", "auto": return L("Через VPN")
        case "dns", "dns-out": return "DNS"
        default: return o
        }
    }

    private func poll() async {
        while !Task.isCancelled {
            await refresh()
            try? await Task.sleep(nanoseconds: 1_500_000_000)
        }
    }

    private func refresh() async {
        now = Date()
        guard tunnel.state == .connected else {
            list = []
            loaded = true
            return
        }
        switch await tunnel.connections() {
        case let .success(l):
            list = l
            error = nil
        case let .failure(e):
            error = e.message
        }
        loaded = true
    }

    private func close(_ id: String) {
        if id.isEmpty { list = [] } else { list.removeAll { $0.id == id } }
        Task {
            await tunnel.closeConnection(id)
            await refresh()
        }
    }
}
