package app.vela.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
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
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import app.vela.client.BuildConfig
import app.vela.client.data.Settings

@Composable
fun SettingsScreen(
    settings: Settings,
    onChange: (Settings) -> Unit,
    onBack: () -> Unit,
    onPrivacy: () -> Unit,
    onSystemVpnSettings: () -> Unit,
) {
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
        )
        if (next != latest) onChange(next)
    }
    DisposableEffect(Unit) { onDispose { commit() } }

    Column(Modifier.fillMaxSize().background(Vela.Ink).safeDrawingPadding()) {
        Row(Modifier.fillMaxWidth().padding(start = 4.dp, top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Назад") }
            Text("Настройки", style = MaterialTheme.typography.titleLarge)
        }
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 8.dp)) {
            Group("Фильтры") {
                SwitchRow("Блокировать рекламу", "Рекламные и следящие домены не загружаются", settings.blockAds) {
                    onChange(settings.copy(blockAds = it))
                }
                Divider()
                SwitchRow("IPv6", "Выключите, если сайты открываются медленно", settings.ipv6) { onChange(settings.copy(ipv6 = it)) }
                Divider()
                Field("Всегда напрямую", direct, "gosuslugi.ru, sberbank.ru") { direct = it }
                Field("Всегда через VPN", proxy, "youtube.com, chatgpt.com") { proxy = it }
            }
            Group("Система") {
                ActionRow("Постоянный VPN", "Включите «Постоянная VPN» и «Блокировать соединения без VPN» в настройках Android", onSystemVpnSettings)
            }
            Group("Для опытных") {
                if (!advanced) {
                    TextButton(onClick = { advanced = true }) { Text("Показать", color = Vela.Muted) }
                } else {
                    Field("DNS через VPN", remoteDns, "https://1.1.1.1/dns-query") { remoteDns = it }
                    Field("DNS для прямых сайтов", directDns, "77.88.8.8") { directDns = it }
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
                }
            }
            Group("О программе") {
                Text(
                    "Vela ${BuildConfig.VERSION_NAME}. Открытый исходный код под лицензией GPL-3.0. " +
                        "Vela не собирает статистику и не передаёт данные о вашем трафике.",
                    color = Vela.Muted, fontSize = 13.5.sp, modifier = Modifier.padding(vertical = 8.dp),
                )
                TextButton(onClick = onPrivacy) { Text("Политика конфиденциальности", color = Vela.Star) }
            }
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
