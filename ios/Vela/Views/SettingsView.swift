import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var store: Store
    @EnvironmentObject var tunnel: TunnelManager
    @Environment(\.dismiss) private var dismiss
    @State private var s = VelaSettings()
    @State private var direct = ""
    @State private var proxy = ""
    @State private var showPrivacy = false
    @State private var showLogs = false
    @State private var showImport = false
    @State private var sharing: ShareItem?
    @State private var message: String?

    var body: some View {
        NavigationStack {
            Form {
                Section("Подключение") {
                    Toggle(isOn: Binding(get: { !s.disableFailover }, set: { s.disableFailover = !$0 })) {
                        VStack(alignment: .leading) {
                            Text("Автопереключение при сбое")
                            Text("Если сервер перестал отвечать, Vela сама перейдёт на рабочий").font(.caption).foregroundStyle(Palette.muted)
                        }
                    }
                }
                Section("Оформление") {
                    Picker("Тема", selection: Binding(get: { store.state.theme }, set: { store.setTheme($0) })) {
                        Text("Как в системе").tag("system")
                        Text("Светлая").tag("light")
                        Text("Тёмная").tag("dark")
                    }
                    .pickerStyle(.segmented)
                }
                Section {
                    Button("Экспортировать") {
                        do {
                            sharing = ShareItem(title: "Резервная копия",
                                                hint: "Отсканируйте QR-код в Vela на другом устройстве или отправьте текст и вставьте его в окно «Добавить».",
                                                text: try store.exportBackup())
                        } catch { message = error.localizedDescription }
                    }
                    Button("Импортировать") { showImport = true }
                } header: { Text("Резервная копия") } footer: {
                    Text("Подписки, свои серверы, избранное и настройки одним текстом или QR-кодом.")
                }
                Section("Фильтры") {
                    Toggle(isOn: $s.blockAds) {
                        VStack(alignment: .leading) {
                            Text("Блокировать рекламу")
                            Text("Рекламные и следящие домены не загружаются").font(.caption).foregroundStyle(Palette.muted)
                        }
                    }
                    Toggle(isOn: $s.ipv6) {
                        VStack(alignment: .leading) {
                            Text("IPv6")
                            Text("Выключите, если сайты открываются медленно").font(.caption).foregroundStyle(Palette.muted)
                        }
                    }
                }
                Section {
                    TextField("gosuslugi.ru, sberbank.ru", text: $direct, axis: .vertical).textInputAutocapitalization(.never).autocorrectionDisabled()
                } header: { Text("Всегда напрямую") }
                Section {
                    TextField("youtube.com, chatgpt.com", text: $proxy, axis: .vertical).textInputAutocapitalization(.never).autocorrectionDisabled()
                } header: { Text("Всегда через VPN") }
                Section("Для опытных") {
                    LabeledContent("DNS через VPN") {
                        TextField("", text: $s.remoteDNS).multilineTextAlignment(.trailing).textInputAutocapitalization(.never).autocorrectionDisabled()
                    }
                    LabeledContent("DNS для прямых") {
                        TextField("", text: $s.directDNS).multilineTextAlignment(.trailing).textInputAutocapitalization(.never).autocorrectionDisabled()
                    }
                    Picker("Фрагментация TLS", selection: $s.tlsFragment) {
                        Text("Выключена").tag("")
                        Text("Лёгкая").tag("record")
                        Text("Сильная").tag("full")
                    }
                    Button("Журнал ядра") { showLogs = true }
                }
                Section {
                    ForEach($s.rules) { $rule in
                        HStack {
                            TextField("example.com", text: $rule.match).textInputAutocapitalization(.never).autocorrectionDisabled()
                            Picker("", selection: $rule.action) {
                                Text("Напрямую").tag("direct")
                                Text("Через VPN").tag("proxy")
                                Text("Блокировать").tag("block")
                            }
                            .labelsHidden()
                            .fixedSize()
                        }
                    }
                    .onDelete { s.rules.remove(atOffsets: $0) }
                    Button("Добавить правило") { s.rules.append(RoutingRule(match: "")) }
                } header: { Text("Свои правила") } footer: {
                    Text("Домен (example.com — вместе с поддоменами), слово в звёздочках (*ads*) или IP/подсеть. Проверяются первыми. Смахните влево, чтобы удалить.")
                }
                Section("О программе") {
                    Text("Vela \(Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""). Открытый исходный код под лицензией GPL-3.0. Vela не собирает статистику и не передаёт данные о вашем трафике.")
                        .font(.footnote).foregroundStyle(Palette.muted)
                    Button("Политика конфиденциальности") { showPrivacy = true }
                }
            }
            .scrollContentBackground(.hidden)
            .background(Palette.ink)
            .navigationTitle("Настройки")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Готово") { save(); dismiss() } } }
            .sheet(isPresented: $showLogs) { LogsSheet() }
            .sheet(item: $sharing) { item in ShareSheet(title: item.title, hint: item.hint, text: item.text) }
            .sheet(isPresented: $showImport) {
                AddSheet(initial: "") { msg in
                    if let msg {
                        message = msg
                        s = store.state.settings
                        direct = s.directDomains.joined(separator: ", ")
                        proxy = s.proxyDomains.joined(separator: ", ")
                        Task { await tunnel.reload() }
                    }
                }
            }
            .alert(message ?? "", isPresented: Binding(get: { message != nil }, set: { if !$0 { message = nil } })) {
                Button("OK", role: .cancel) {}
            }
            .alert("Конфиденциальность", isPresented: $showPrivacy) {
                Button("Понятно", role: .cancel) {}
            } message: {
                Text("Vela хранит серверы и настройки только на этом устройстве и исключает их из резервных копий. Приложение не собирает аналитику, не показывает рекламу и не отправляет данные о посещаемых сайтах ни нам, ни третьим лицам. Трафик идёт на сервер, который вы добавили сами.")
            }
        }
        .tint(Palette.star)
        .onAppear {
            s = store.state.settings
            direct = s.directDomains.joined(separator: ", ")
            proxy = s.proxyDomains.joined(separator: ", ")
        }
        .onDisappear(perform: save)
    }

    private func domains(_ text: String) -> [String] {
        text.components(separatedBy: CharacterSet(charactersIn: ", ;\n"))
            .map { $0.replacingOccurrences(of: "https://", with: "").replacingOccurrences(of: "http://", with: "").components(separatedBy: "/")[0] }
            .filter { !$0.isEmpty }
    }

    private func save() {
        var next = s
        next.directDomains = domains(direct)
        next.proxyDomains = domains(proxy)
        if next.remoteDNS.isEmpty { next.remoteDNS = VelaSettings().remoteDNS }
        if next.directDNS.isEmpty { next.directDNS = VelaSettings().directDNS }
        next.rules = next.rules.map { var r = $0; r.match = r.match.trimmingCharacters(in: .whitespaces); return r }.filter { !$0.match.isEmpty }
        guard next != store.state.settings else { return }
        store.setSettings(next)
        Task { await tunnel.reload() }
    }
}
