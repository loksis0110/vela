package app.vela.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.RadioButton
import androidx.compose.material3.RadioButtonDefaults
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import app.vela.client.BuildConfig
import app.vela.client.data.Rule
import app.vela.client.data.Settings
import app.vela.client.data.SubPrefs

@Composable
fun SettingsScreen(
    settings: Settings,
    onChange: (Settings) -> Unit,
    onBack: () -> Unit,
    onPrivacy: () -> Unit,
    onSystemVpnSettings: () -> Unit,
    theme: String,
    onTheme: (String) -> Unit,
    onExport: () -> Unit,
    onImport: () -> Unit,
    sub: SubPrefs,
    onSub: (SubPrefs) -> Unit,
    connectOnLaunch: Boolean,
    onConnectOnLaunch: (Boolean) -> Unit,
    onReset: () -> Unit,
) {
    var ua by rememberSaveable { mutableStateOf(sub.userAgent) }
    var confirmReset by remember { mutableStateOf(false) }
    val latestSub by rememberUpdatedState(sub)
    DisposableEffect(Unit) { onDispose { if (ua.trim() != latestSub.userAgent) onSub(latestSub.copy(userAgent = ua.trim())) } }
    val context = LocalContext.current
    var picking by remember { mutableStateOf(false) }
    var logs by remember { mutableStateOf(false) }
    var rules by remember { mutableStateOf(settings.rules) }
    // text fields are saved only if the user edited them, so a backup restored
    // from this screen isn't overwritten by the old values
    var dirty by remember { mutableStateOf(false) }
    var advanced by rememberSaveable { mutableStateOf(false) }
    var direct by rememberSaveable { mutableStateOf(settings.directDomains.joinToString(", ")) }
    var proxy by rememberSaveable { mutableStateOf(settings.proxyDomains.joinToString(", ")) }
    var remoteDns by rememberSaveable { mutableStateOf(settings.remoteDns) }
    var directDns by rememberSaveable { mutableStateOf(settings.directDns) }

    fun domains(s: String) = s.split(',', ' ', ';', '\n').map { it.trim().removePrefix("https://").removePrefix("http://").substringBefore('/') }
        .filter { it.isNotEmpty() }

    // text fields are committed when the screen closes, so typing doesn't reconnect the VPN
    val latest by rememberUpdatedState(settings)
    val commit by rememberUpdatedState {
        val next = latest.copy(
            directDomains = domains(direct), proxyDomains = domains(proxy),
            remoteDns = remoteDns.trim().ifEmpty { Settings().remoteDns },
            directDns = directDns.trim().ifEmpty { Settings().directDns },
            rules = rules.map { it.copy(match = it.match.trim()) }.filter { it.match.isNotEmpty() },
        )
        if (next != latest) onChange(next)
    }
    DisposableEffect(Unit) { onDispose { if (dirty) commit() } }
    // settings changed elsewhere (restored backup): show them
    LaunchedEffect(settings) {
        if (!dirty) {
            direct = settings.directDomains.joinToString(", ")
            proxy = settings.proxyDomains.joinToString(", ")
            remoteDns = settings.remoteDns
            directDns = settings.directDns
            rules = settings.rules
        }
    }

    Column(Modifier.fillMaxSize().background(Vela.Ink).safeDrawingPadding()) {
        Row(Modifier.fillMaxWidth().padding(start = 4.dp, top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Назад") }
            Text("Настройки", style = MaterialTheme.typography.titleLarge)
        }
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 8.dp)) {
            Group("Подключение") {
                SwitchRow("Подключаться при запуске", "Vela подключится сама, когда вы её откроете", connectOnLaunch, onConnectOnLaunch)
                Divider()
                SwitchRow(
                    "Автопереключение при сбое", "Если сервер перестал отвечать, Vela сама перейдёт на рабочий",
                    !settings.disableFailover,
                ) { onChange(settings.copy(disableFailover = !it)) }
            }
            Group("Оформление") {
                Segmented(
                    listOf("system" to "Как в системе", "light" to "Светлая", "dark" to "Тёмная"), theme, onTheme,
                    Modifier.padding(vertical = 8.dp),
                )
            }
            Group("Раздельное туннелирование") {
                Text("Какие приложения идут через VPN", color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp))
                listOf("" to "Все приложения", "exclude" to "Все, кроме выбранных", "only" to "Только выбранные").forEach { (v, l) ->
                    Row(
                        Modifier.fillMaxWidth().clickable(role = Role.RadioButton) {
                            if (v != settings.appMode) {
                                if (v != "" && settings.apps.isEmpty()) picking = true
                                onChange(settings.copy(appMode = v))
                            }
                        }.padding(vertical = 2.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        RadioButton(settings.appMode == v, null, colors = RadioButtonDefaults.colors(selectedColor = Vela.Star))
                        Text(l, modifier = Modifier.padding(start = 8.dp))
                    }
                }
                if (settings.appMode.isNotEmpty()) {
                    val names = settings.apps.take(4).joinToString(", ") { appLabel(context, it) } +
                        if (settings.apps.size > 4) " и ещё ${settings.apps.size - 4}" else ""
                    ActionRow(
                        if (settings.apps.isEmpty()) "Выбрать приложения" else "Приложения: ${settings.apps.size}",
                        names.ifEmpty { "Ни одного не выбрано" },
                    ) { picking = true }
                }
            }
            Group("Подписки") {
                SwitchRow("Обновлять автоматически", "Списки серверов обновляются при открытии приложения", !sub.noAutoUpdate) {
                    onSub(sub.copy(noAutoUpdate = !it))
                }
                if (!sub.noAutoUpdate) {
                    Text("Как часто", color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 6.dp))
                    Segmented(
                        listOf("0" to "Как у провайдера", "3" to "3 ч", "12" to "12 ч", "24" to "Сутки"),
                        sub.updateHours.toString(), { onSub(sub.copy(updateHours = it.toInt())) },
                        Modifier.padding(vertical = 8.dp),
                    )
                }
                Divider()
                SwitchRow(
                    "Отправлять HWID",
                    "Случайный номер этой установки, модель телефона и версия Android. Нужен провайдерам с лимитом устройств, личных данных нет",
                    !sub.noHwid,
                ) { onSub(sub.copy(noHwid = !it)) }
                Field("User-Agent", ua, "по умолчанию Vela") { ua = it }
                Text(
                    "Меняйте, только если провайдер просит определённый клиент. Затем обновите подписку.",
                    color = Vela.Muted, fontSize = 12.sp, modifier = Modifier.padding(bottom = 6.dp),
                )
            }
            Group("Резервная копия") {
                Text(
                    "Подписки, свои серверы, избранное и настройки одним текстом или QR-кодом.",
                    color = Vela.Muted, fontSize = 12.5.sp, modifier = Modifier.padding(top = 8.dp),
                )
                Row {
                    TextButton(onClick = onExport) { Text("Экспортировать", color = Vela.Star) }
                    TextButton(onClick = onImport) { Text("Импортировать", color = Vela.Star) }
                }
            }
            Group("Фильтры") {
                SwitchRow("Блокировать рекламу", "Рекламные и следящие домены не загружаются", settings.blockAds) {
                    onChange(settings.copy(blockAds = it))
                }
                Divider()
                SwitchRow("IPv6", "Выключите, если сайты открываются медленно", settings.ipv6) { onChange(settings.copy(ipv6 = it)) }
                Divider()
                Field("Всегда напрямую", direct, "gosuslugi.ru, sberbank.ru") { direct = it; dirty = true }
                Field("Всегда через VPN", proxy, "youtube.com, chatgpt.com") { proxy = it; dirty = true }
            }
            Group("Система") {
                ActionRow("Постоянный VPN", "Включите «Постоянная VPN» и «Блокировать соединения без VPN» в настройках Android", onSystemVpnSettings)
            }
            Group("Для опытных") {
                if (!advanced) {
                    TextButton(onClick = { advanced = true }) { Text("Показать", color = Vela.Muted) }
                } else {
                    Field("DNS через VPN", remoteDns, "https://1.1.1.1/dns-query") { remoteDns = it; dirty = true }
                    Field("DNS для прямых сайтов", directDns, "77.88.8.8") { directDns = it; dirty = true }
                    Text("Сетевой стек", color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 10.dp))
                    listOf("mixed" to "Смешанный (рекомендуется)", "system" to "Системный (быстрее)", "gvisor" to "gVisor (совместимее)").forEach { (v, l) ->
                        Row(
                            Modifier.fillMaxWidth().clickable(role = Role.RadioButton) { onChange(settings.copy(stack = v)) }.padding(vertical = 2.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            RadioButton(settings.stack == v, null, colors = RadioButtonDefaults.colors(selectedColor = Vela.Star))
                            Text(l, modifier = Modifier.padding(start = 8.dp))
                        }
                    }
                    Divider()
                    Text("Фрагментация TLS", color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 10.dp))
                    Text(
                        "Разбивает начало TLS-соединения, чтобы обойти простую блокировку. Включайте, только если без неё не работает.",
                        color = Vela.Muted, fontSize = 12.sp,
                    )
                    listOf("" to "Выключена", "record" to "Лёгкая", "full" to "Сильная (медленнее)").forEach { (v, l) ->
                        Row(
                            Modifier.fillMaxWidth().clickable(role = Role.RadioButton) { onChange(settings.copy(tlsFragment = v)) }.padding(vertical = 2.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            RadioButton(settings.tlsFragment == v, null, colors = RadioButtonDefaults.colors(selectedColor = Vela.Star))
                            Text(l, modifier = Modifier.padding(start = 8.dp))
                        }
                    }
                    Divider()
                    Text("Свои правила", color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 10.dp))
                    Text(
                        "Домен (example.com — вместе с поддоменами), слово в звёздочках (*ads*) или IP/подсеть. Проверяются первыми.",
                        color = Vela.Muted, fontSize = 12.sp,
                    )
                    rules.forEachIndexed { i, r ->
                        RuleRow(
                            r,
                            onChange = { nr -> rules = rules.toMutableList().also { it[i] = nr }; dirty = true },
                            onDelete = { rules = rules.toMutableList().also { it.removeAt(i) }; dirty = true },
                        )
                    }
                    TextButton(onClick = { rules = rules + Rule("", "direct"); dirty = true }) { Text("+ Добавить правило", color = Vela.Star) }
                    Divider()
                    ActionRow("Журнал ядра", "Что происходит с подключением. Пригодится, если что-то не работает") { logs = true }
                }
            }
            Group("О программе") {
                Text(
                    "Vela ${BuildConfig.VERSION_NAME}. Открытый исходный код под лицензией GPL-3.0. " +
                        "Vela не собирает статистику и не передаёт данные о вашем трафике.",
                    color = Vela.Muted, fontSize = 13.5.sp, modifier = Modifier.padding(vertical = 8.dp),
                )
                TextButton(onClick = onPrivacy) { Text("Политика конфиденциальности", color = Vela.Star) }
                TextButton(onClick = { confirmReset = true }) { Text("Сбросить настройки", color = Vela.Danger) }
            }
        }
    }

    if (picking) {
        AppPickerDialog(
            selected = settings.apps,
            onDismiss = {
                picking = false
                // «only selected» with nothing selected would quietly tunnel everything
                if (settings.apps.isEmpty() && settings.appMode.isNotEmpty()) onChange(settings.copy(appMode = ""))
            },
            onDone = { list ->
                picking = false
                onChange(settings.copy(apps = list, appMode = if (list.isEmpty()) "" else settings.appMode.ifEmpty { "exclude" }))
            },
        )
    }
    if (logs) LogsDialog { logs = false }
    if (confirmReset) {
        AlertDialog(
            onDismissRequest = { confirmReset = false },
            containerColor = Vela.Deep,
            title = { Text("Сбросить настройки?") },
            text = { Text("Все настройки вернутся по умолчанию. Подписки, серверы и избранное останутся.") },
            confirmButton = { TextButton(onClick = { confirmReset = false; onReset() }) { Text("Сбросить", color = Vela.Danger) } },
            dismissButton = { TextButton(onClick = { confirmReset = false }) { Text("Отмена") } },
        )
    }
}

@Composable
private fun RuleRow(rule: Rule, onChange: (Rule) -> Unit, onDelete: () -> Unit) {
    var menu by remember { mutableStateOf(false) }
    val actions = listOf("direct" to "Напрямую", "proxy" to "Через VPN", "block" to "Блокировать")
    Row(Modifier.fillMaxWidth().padding(top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        OutlinedTextField(
            value = rule.match, onValueChange = { onChange(rule.copy(match = it)) }, singleLine = true,
            placeholder = { Text("example.com", color = Vela.Muted, fontSize = 13.sp) },
            textStyle = androidx.compose.ui.text.TextStyle(fontSize = 13.sp, color = Vela.Text),
            colors = OutlinedTextFieldDefaults.colors(focusedBorderColor = Vela.Star, unfocusedBorderColor = Vela.Line, cursorColor = Vela.Star),
            modifier = Modifier.weight(1f),
        )
        Box {
            TextButton(onClick = { menu = true }) {
                Text(actions.firstOrNull { it.first == rule.action }?.second ?: "Напрямую", color = Vela.Text, fontSize = 13.sp)
            }
            DropdownMenu(menu, { menu = false }, containerColor = Vela.Deep) {
                actions.forEach { (v, l) -> DropdownMenuItem({ Text(l) }, { onChange(rule.copy(action = v)); menu = false }) }
            }
        }
        IconButton(onClick = onDelete, modifier = Modifier.size(36.dp)) {
            Icon(Icons.Rounded.Close, "Удалить правило", tint = Vela.Muted, modifier = Modifier.size(18.dp))
        }
    }
}

@Composable
private fun Segmented(options: List<Pair<String, String>>, value: String, onChange: (String) -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Vela.Ink).padding(3.dp),
        horizontalArrangement = Arrangement.spacedBy(3.dp),
    ) {
        options.forEach { (v, l) ->
            val on = v == value
            Text(
                l, fontSize = 13.sp, fontWeight = if (on) FontWeight.SemiBold else FontWeight.Normal,
                color = if (on) Vela.Text else Vela.Muted, textAlign = TextAlign.Center, maxLines = 1,
                modifier = Modifier.weight(1f).clip(RoundedCornerShape(11.dp))
                    .background(if (on) Vela.Hull else androidx.compose.ui.graphics.Color.Transparent)
                    .clickable(role = Role.RadioButton) { onChange(v) }
                    .padding(vertical = 9.dp),
            )
        }
    }
}

@Composable
private fun Group(title: String, content: @Composable ColumnScope.() -> Unit) {
    Text(title, color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(start = 6.dp, top = 10.dp, bottom = 6.dp))
    Column(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(22.dp)).background(Vela.Deep)
            .border(1.dp, Vela.Hull, RoundedCornerShape(22.dp)).padding(horizontal = 14.dp, vertical = 6.dp),
        content = content,
    )
}

@Composable
private fun Divider() = HorizontalDivider(color = Vela.Hull)

@Composable
private fun SwitchRow(title: String, sub: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(role = Role.Switch) { onChange(!checked) }.padding(vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(title, fontWeight = FontWeight.SemiBold, fontSize = 14.sp)
            Text(sub, color = Vela.Muted, fontSize = 12.5.sp)
        }
        Switch(
            checked, null,
            colors = SwitchDefaults.colors(
                checkedTrackColor = Vela.Sea, checkedThumbColor = Vela.OnSea,
                uncheckedTrackColor = Vela.Hull, uncheckedThumbColor = Vela.Muted, uncheckedBorderColor = Vela.Hull,
            ),
        )
    }
}

@Composable
private fun ActionRow(title: String, sub: String, onClick: () -> Unit) {
    Column(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(vertical = 10.dp)) {
        Text(title, fontWeight = FontWeight.SemiBold, fontSize = 14.sp)
        Text(sub, color = Vela.Muted, fontSize = 12.5.sp)
    }
}

@Composable
private fun Field(label: String, value: String, placeholder: String, onChange: (String) -> Unit) {
    OutlinedTextField(
        value = value, onValueChange = onChange,
        label = { Text(label) }, placeholder = { Text(placeholder, color = Vela.Muted) },
        singleLine = false, maxLines = 3,
        colors = OutlinedTextFieldDefaults.colors(
            focusedBorderColor = Vela.Star, unfocusedBorderColor = Vela.Line,
            focusedLabelColor = Vela.Star, unfocusedLabelColor = Vela.Muted,
            cursorColor = Vela.Star,
        ),
        modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp),
    )
}
