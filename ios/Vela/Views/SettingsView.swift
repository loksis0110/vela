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
    @State private var showDiag = false
    @State private var showImport = false
    @State private var sharing: ShareItem?
    @State private var message: String?
    @State private var sub = SubPrefs()
    @State private var confirmReset = false

    var body: some View {
        NavigationStack {
            Form {
                Section("Подключение") {
                    Toggle(isOn: Binding(get: { store.state.connectOnLaunch }, set: { store.setConnectOnLaunch($0) })) {
                        VStack(alignment: .leading) {
                            Text("Подключаться при запуске")
                            Text("Vela подключится сама, когда вы её откроете").font(.caption).foregroundStyle(Palette.muted)
                        }
                    }
                    Toggle(isOn: Binding(get: { store.state.alwaysOn }, set: { on in
                        store.setAlwaysOn(on)
                        Task { await tunnel.applyAlwaysOn() }
                    })) {
                        VStack(alignment: .leading) {
                            Text("Постоянный VPN")
                            Text("iOS сама восстановит подключение после смены сети или перезагрузки, пока вы не нажмёте «Отключить»")
                                .font(.caption).foregroundStyle(Palette.muted)
                        }
                    }
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
                    Toggle(isOn: Binding(get: { !sub.noAutoUpdate }, set: { sub.noAutoUpdate = !$0; store.setSubPrefs(sub) })) {
                        Text("Обновлять автоматически")
                    }
                    if !sub.noAutoUpdate {
                        Picker("Как часто", selection: Binding(get: { sub.updateHours }, set: { sub.updateHours = $0; store.setSubPrefs(sub) })) {
                            Text("Как у провайдера").tag(0)
                            Text("Каждые 3 часа").tag(3)
                            Text("Каждые 12 часов").tag(12)
                            Text("Раз в сутки").tag(24)
                        }
                    }
                    Toggle(isOn: Binding(get: { !sub.noHWID }, set: { sub.noHWID = !$0; store.setSubPrefs(sub) })) {
                        VStack(alignment: .leading) {
                            Text("Отправлять HWID")
                            Text("Случайный номер этой установки, модель iPhone и версия iOS. Нужен провайдерам с лимитом устройств, личных данных нет")
                                .font(.caption).foregroundStyle(Palette.muted)
                        }
                    }
                    LabeledContent("User-Agent") {
                        TextField("по умолчанию Vela", text: $sub.userAgent)
                            .multilineTextAlignment(.trailing).textInputAutocapitalization(.never).autocorrectionDisabled()
                            .onSubmit { store.setSubPrefs(sub) }
                    }
                } header: { Text("Подписки") } footer: {
                    Text("Меняйте User-Agent, только если провайдер просит определённый клиент. Затем обновите подписку.")
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
                    Button("Диагностика подключения") { showDiag = true }
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
                    Button("Сбросить настройки", role: .destructive) { confirmReset = true }
                }
            }
            .scrollContentBackground(.hidden)
            .background(Palette.ink)
            .navigationTitle("Настройки")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Готово") { save(); dismiss() } } }
            .sheet(isPresented: $showLogs) { LogsSheet() }
            .sheet(isPresented: $showDiag) { DiagnosticsSheet() }
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
            .confirmationDialog("Сбросить настройки?", isPresented: $confirmReset, titleVisibility: .visible) {
                Button("Сбросить", role: .destructive) {
                    store.resetSettings()
                    s = store.state.settings
                    sub = store.state.sub
                    direct = ""
                    proxy = ""
                    applyTheme(store.state.theme)
                    Task {
                        await tunnel.applyAlwaysOn()
                        await tunnel.reload()
                    }
                }
            } message: {
                Text("Все настройки вернутся по умолчанию. Подписки, серверы и избранное останутся.")
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
            sub = store.state.sub
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
        if sub != store.state.sub { store.setSubPrefs(sub) }
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
