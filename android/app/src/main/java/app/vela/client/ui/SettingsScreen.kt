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
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import app.vela.client.BuildConfig
import app.vela.client.R
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
    language: String,
    onLanguage: (String) -> Unit,
    clipboard: Boolean,
    onClipboard: (Boolean) -> Unit,
    reminders: Boolean,
    onReminders: (Boolean) -> Unit,
) {
    var ua by rememberSaveable { mutableStateOf(sub.userAgent) }
    var confirmReset by remember { mutableStateOf(false) }
    val latestSub by rememberUpdatedState(sub)
    DisposableEffect(Unit) { onDispose { if (ua.trim() != latestSub.userAgent) onSub(latestSub.copy(userAgent = ua.trim())) } }
    val context = LocalContext.current
    var picking by remember { mutableStateOf(false) }
    // «exclude»/«only» chosen with no apps yet: applied once apps are picked, so
    // the VPN isn't reloaded twice for nothing (and «only» never means «all»)
    var pendingMode by remember { mutableStateOf<String?>(null) }
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
            IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, stringResource(R.string.back)) }
            Text(stringResource(R.string.settings_title), style = MaterialTheme.typography.titleLarge)
        }
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 8.dp)) {
            Group(stringResource(R.string.set_routing)) {
                routingModes.forEach { m ->
                    Row(
                        Modifier.fillMaxWidth().clickable(role = Role.RadioButton) {
                            if (m.value != settings.routing) onChange(settings.copy(routing = m.value))
                        }.padding(vertical = 6.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        RadioButton(settings.routing == m.value, null, colors = RadioButtonDefaults.colors(selectedColor = Vela.Star))
                        Column(Modifier.padding(start = 8.dp)) {
                            Text(stringResource(m.title), fontWeight = FontWeight.SemiBold, fontSize = 14.sp)
                            Text(stringResource(m.hint), color = Vela.Muted, fontSize = 12.5.sp)
                        }
                    }
                }
            }
            Group(stringResource(R.string.set_connection)) {
                SwitchRow(stringResource(R.string.set_connect_on_launch), stringResource(R.string.set_connect_on_launch_hint), connectOnLaunch, onConnectOnLaunch)
                Divider()
                SwitchRow(
                    stringResource(R.string.set_failover), stringResource(R.string.set_failover_hint),
                    !settings.disableFailover,
                ) { onChange(settings.copy(disableFailover = !it)) }
            }
            Group(stringResource(R.string.set_appearance)) {
                Segmented(
                    listOf(
                        "system" to stringResource(R.string.theme_system),
                        "light" to stringResource(R.string.theme_light),
                        "dark" to stringResource(R.string.theme_dark),
                    ),
                    theme, onTheme, Modifier.padding(vertical = 8.dp),
                )
                Text(stringResource(R.string.set_language), color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 4.dp))
                Segmented(
                    // language names are shown in their own language
                    listOf("system" to stringResource(R.string.lang_system), "ru" to "Русский", "en" to "English"),
                    language, onLanguage, Modifier.padding(vertical = 8.dp),
                )
            }
            Group(stringResource(R.string.set_split)) {
                Text(stringResource(R.string.set_split_hint), color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp))
                listOf(
                    "" to stringResource(R.string.split_all),
                    "exclude" to stringResource(R.string.split_exclude),
                    "only" to stringResource(R.string.split_only),
                ).forEach { (v, l) ->
                    Row(
                        Modifier.fillMaxWidth().clickable(role = Role.RadioButton) {
                            if (v != settings.appMode) {
                                if (v != "" && settings.apps.isEmpty()) {
                                    pendingMode = v
                                    picking = true
                                } else {
                                    onChange(settings.copy(appMode = v))
                                }
                            }
                        }.padding(vertical = 2.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        RadioButton((pendingMode ?: settings.appMode) == v, null, colors = RadioButtonDefaults.colors(selectedColor = Vela.Star))
                        Text(l, modifier = Modifier.padding(start = 8.dp))
                    }
                }
                if (settings.appMode.isNotEmpty()) {
                    val names = settings.apps.take(4).joinToString(", ") { appLabel(context, it) } +
                        if (settings.apps.size > 4) " " + stringResource(R.string.split_more, settings.apps.size - 4) else ""
                    ActionRow(
                        if (settings.apps.isEmpty()) stringResource(R.string.split_choose) else stringResource(R.string.split_count, settings.apps.size),
                        names.ifEmpty { stringResource(R.string.split_none) },
                    ) { picking = true }
                }
            }
            Group(stringResource(R.string.set_subscriptions)) {
                SwitchRow(stringResource(R.string.set_auto_update), stringResource(R.string.set_auto_update_hint), !sub.noAutoUpdate) {
                    onSub(sub.copy(noAutoUpdate = !it))
                }
                if (!sub.noAutoUpdate) {
                    Text(stringResource(R.string.set_how_often), color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 6.dp))
                    Segmented(
                        listOf(
                            "0" to stringResource(R.string.interval_provider),
                            "3" to stringResource(R.string.interval_3h),
                            "12" to stringResource(R.string.interval_12h),
                            "24" to stringResource(R.string.interval_day),
                        ),
                        sub.updateHours.toString(), { onSub(sub.copy(updateHours = it.toInt())) },
                        Modifier.padding(vertical = 8.dp),
                    )
                }
                Divider()
                SwitchRow(stringResource(R.string.set_reminders), stringResource(R.string.set_reminders_hint), reminders, onReminders)
                Divider()
                SwitchRow(stringResource(R.string.set_clipboard), stringResource(R.string.set_clipboard_hint), clipboard, onClipboard)
                Divider()
                SwitchRow(
                    stringResource(R.string.set_hwid),
                    stringResource(R.string.set_hwid_hint),
                    !sub.noHwid,
                ) { onSub(sub.copy(noHwid = !it)) }
                Field("User-Agent", ua, stringResource(R.string.set_ua_placeholder)) { ua = it }
                Text(
                    stringResource(R.string.set_ua_hint),
                    color = Vela.Muted, fontSize = 12.sp, modifier = Modifier.padding(bottom = 6.dp),
                )
            }
            Group(stringResource(R.string.set_backup)) {
                Text(
                    stringResource(R.string.set_backup_hint),
                    color = Vela.Muted, fontSize = 12.5.sp, modifier = Modifier.padding(top = 8.dp),
                )
                Row {
                    TextButton(onClick = onExport) { Text(stringResource(R.string.export), color = Vela.Star) }
                    TextButton(onClick = onImport) { Text(stringResource(R.string.import_action), color = Vela.Star) }
                }
            }
            Group(stringResource(R.string.set_filters)) {
                if (!BuildConfig.PLAY) {
                    SwitchRow(stringResource(R.string.set_block_ads), stringResource(R.string.set_block_ads_hint), settings.blockAds) {
                        onChange(settings.copy(blockAds = it))
                    }
                    Divider()
                }
                SwitchRow("IPv6", stringResource(R.string.set_ipv6_hint), settings.ipv6) { onChange(settings.copy(ipv6 = it)) }
                Divider()
                Field(stringResource(R.string.set_always_direct), direct, "gosuslugi.ru, sberbank.ru") { direct = it; dirty = true }
                Field(stringResource(R.string.set_always_proxy), proxy, "youtube.com, chatgpt.com") { proxy = it; dirty = true }
            }
            Group(stringResource(R.string.set_system)) {
                ActionRow(stringResource(R.string.set_always_on), stringResource(R.string.set_always_on_hint), onSystemVpnSettings)
            }
            Group(stringResource(R.string.set_advanced)) {
                if (!advanced) {
                    TextButton(onClick = { advanced = true }) { Text(stringResource(R.string.show), color = Vela.Muted) }
                } else {
                    Field(stringResource(R.string.set_remote_dns), remoteDns, "https://1.1.1.1/dns-query") { remoteDns = it; dirty = true }
                    Field(stringResource(R.string.set_direct_dns), directDns, "77.88.8.8") { directDns = it; dirty = true }
                    Text(stringResource(R.string.set_stack), color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 10.dp))
                    listOf(
                        "mixed" to stringResource(R.string.stack_mixed),
                        "system" to stringResource(R.string.stack_system),
                        "gvisor" to stringResource(R.string.stack_gvisor),
                    ).forEach { (v, l) ->
                        Row(
                            Modifier.fillMaxWidth().clickable(role = Role.RadioButton) { onChange(settings.copy(stack = v)) }.padding(vertical = 2.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            RadioButton(settings.stack == v, null, colors = RadioButtonDefaults.colors(selectedColor = Vela.Star))
                            Text(l, modifier = Modifier.padding(start = 8.dp))
                        }
                    }
                    Divider()
                    Text(stringResource(R.string.set_fragment), color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 10.dp))
                    Text(stringResource(R.string.set_fragment_hint), color = Vela.Muted, fontSize = 12.sp)
                    listOf(
                        "" to stringResource(R.string.fragment_off),
                        "record" to stringResource(R.string.fragment_light),
                        "full" to stringResource(R.string.fragment_full),
                    ).forEach { (v, l) ->
                        Row(
                            Modifier.fillMaxWidth().clickable(role = Role.RadioButton) { onChange(settings.copy(tlsFragment = v)) }.padding(vertical = 2.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            RadioButton(settings.tlsFragment == v, null, colors = RadioButtonDefaults.colors(selectedColor = Vela.Star))
                            Text(l, modifier = Modifier.padding(start = 8.dp))
                        }
                    }
                    Divider()
                    Text(stringResource(R.string.set_rules), color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 10.dp))
                    Text(stringResource(R.string.set_rules_hint), color = Vela.Muted, fontSize = 12.sp)
                    rules.forEachIndexed { i, r ->
                        RuleRow(
                            r,
                            onChange = { nr -> rules = rules.toMutableList().also { it[i] = nr }; dirty = true },
                            onDelete = { rules = rules.toMutableList().also { it.removeAt(i) }; dirty = true },
                        )
                    }
                    TextButton(onClick = { rules = rules + Rule("", "direct"); dirty = true }) { Text(stringResource(R.string.add_rule), color = Vela.Star) }
                    Divider()
                    ActionRow(stringResource(R.string.log_title), stringResource(R.string.set_log_hint)) { logs = true }
                }
            }
            Group(stringResource(R.string.set_about)) {
                Text(
                    stringResource(R.string.set_about_text, BuildConfig.VERSION_NAME),
                    color = Vela.Muted, fontSize = 13.5.sp, modifier = Modifier.padding(vertical = 8.dp),
                )
                TextButton(onClick = onPrivacy) { Text(stringResource(R.string.privacy_policy), color = Vela.Star) }
                TextButton(onClick = { confirmReset = true }) { Text(stringResource(R.string.reset_settings), color = Vela.Danger) }
            }
        }
    }

    if (picking) {
        AppPickerDialog(
            selected = settings.apps,
            onDismiss = {
                picking = false
                pendingMode = null
                // «only selected» with nothing selected would quietly tunnel everything
                if (settings.apps.isEmpty() && settings.appMode.isNotEmpty()) onChange(settings.copy(appMode = ""))
            },
            onDone = { list ->
                picking = false
                val mode = if (list.isEmpty()) "" else (pendingMode ?: settings.appMode).ifEmpty { "exclude" }
                pendingMode = null
                val next = settings.copy(apps = list, appMode = mode)
                if (next != settings) onChange(next)
            },
        )
    }
    if (logs) LogsDialog { logs = false }
    if (confirmReset) {
        AlertDialog(
            onDismissRequest = { confirmReset = false },
            containerColor = Vela.Deep,
            title = { Text(stringResource(R.string.reset_title)) },
            text = { Text(stringResource(R.string.reset_text)) },
            confirmButton = { TextButton(onClick = { confirmReset = false; onReset() }) { Text(stringResource(R.string.reset), color = Vela.Danger) } },
            dismissButton = { TextButton(onClick = { confirmReset = false }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}

@Composable
private fun RuleRow(rule: Rule, onChange: (Rule) -> Unit, onDelete: () -> Unit) {
    var menu by remember { mutableStateOf(false) }
    val actions = listOf(
        "direct" to stringResource(R.string.rule_direct),
        "proxy" to stringResource(R.string.rule_proxy),
        "block" to stringResource(R.string.rule_block),
    )
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
                Text(actions.firstOrNull { it.first == rule.action }?.second ?: actions[0].second, color = Vela.Text, fontSize = 13.sp)
            }
            DropdownMenu(menu, { menu = false }, containerColor = Vela.Deep) {
                actions.forEach { (v, l) -> DropdownMenuItem({ Text(l) }, { onChange(rule.copy(action = v)); menu = false }) }
            }
        }
        IconButton(onClick = onDelete, modifier = Modifier.size(36.dp)) {
            Icon(Icons.Rounded.Close, stringResource(R.string.delete_rule), tint = Vela.Muted, modifier = Modifier.size(18.dp))
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
