package app.vela.client.ui

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.KeyboardArrowRight
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import app.vela.client.R
import app.vela.client.data.AppState
import app.vela.client.vpn.ConnState
import app.vela.client.vpn.VpnStatus
import kotlinx.coroutines.delay

@Composable
fun HomeScreen(
    app: AppState,
    status: VpnStatus,
    pings: Map<String, Int>,
    onToggle: () -> Unit,
    onServers: () -> Unit,
    onSettings: () -> Unit,
    onRouting: (String) -> Unit,
    onDismissError: () -> Unit,
) {
    var now by remember { mutableLongStateOf(System.currentTimeMillis() / 1000) }
    LaunchedEffect(status.state) {
        while (status.state == ConnState.Connected) {
            now = System.currentTimeMillis() / 1000
            delay(1000)
        }
    }

    BoxWithConstraints(
        Modifier
            .fillMaxSize()
            .background(Brush.topGlow())
            .safeDrawingPadding(),
    ) {
    Column(
        Modifier
            .verticalScroll(rememberScrollState())
            .heightIn(min = maxHeight)
            .fillMaxWidth()
            .padding(horizontal = 20.dp),
        verticalArrangement = Arrangement.SpaceBetween,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Image(painterResource(R.drawable.logo), null, Modifier.size(30.dp).clip(RoundedCornerShape(8.dp)))
            Spacer(Modifier.size(10.dp))
            Text("Vela", color = Vela.Star, fontSize = 20.sp, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f))
            IconButton(onClick = onSettings) {
                Icon(Icons.Outlined.Settings, contentDescription = "Настройки", tint = Vela.Muted)
            }
        }

        // ---- constellation control
        val label = when (status.state) {
            ConnState.Connected -> "Отключить"
            ConnState.Connecting -> "Отмена"
            ConnState.Disconnecting -> "Отключение"
            ConnState.Disconnected -> "Подключить"
        }
        Box(
            Modifier
                .padding(top = 8.dp)
                .widthIn(max = 320.dp)
                .fillMaxWidth(0.8f)
                .aspectRatio(1f)
                .clip(CircleShape)
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    role = Role.Button,
                    onClick = onToggle,
                )
                .semantics { contentDescription = label },
            contentAlignment = Alignment.BottomCenter,
        ) {
            Constellation(status.state, Modifier.fillMaxSize())
            val connected = status.state == ConnState.Connected
            Text(
                label,
                color = if (connected) Vela.OnSea else Vela.Text,
                style = MaterialTheme.typography.labelLarge,
                modifier = Modifier
                    .padding(bottom = 14.dp)
                    .clip(RoundedCornerShape(50))
                    .background(if (connected) Vela.Sea else Vela.Hull)
                    .border(1.dp, if (connected) Vela.Sea else Vela.Line, RoundedCornerShape(50))
                    .padding(horizontal = 22.dp, vertical = 9.dp),
            )
        }

        val (statusText, statusColor) = when (status.state) {
            ConnState.Connected -> "Защищено" to Vela.Sea
            ConnState.Connecting -> "Подключение…" to Vela.Flare
            ConnState.Disconnecting -> "Отключение…" to Vela.Text
            ConnState.Disconnected -> "Не подключено" to Vela.Text
        }
        Text(statusText, style = MaterialTheme.typography.headlineSmall, color = statusColor, modifier = Modifier.padding(top = 10.dp))
        val sub = when {
            status.state == ConnState.Connected && status.since > 0 ->
                formatDuration(now - status.since) + ", передано " + formatBytes(status.upTotal + status.downTotal)
            app.profiles.isEmpty() -> "Добавьте сервер, чтобы начать"
            else -> ""
        }
        Text(sub, color = Vela.Muted, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 4.dp).height(20.dp))
        if (status.state == ConnState.Connected) {
            Row(Modifier.padding(top = 10.dp), horizontalArrangement = Arrangement.spacedBy(28.dp)) {
                SpeedLabel("↓", formatSpeed(status.down))
                SpeedLabel("↑", formatSpeed(status.up))
            }
        }

        status.error?.let { err ->
            Row(
                Modifier
                    .padding(top = 16.dp)
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(14.dp))
                    .background(Vela.Danger.copy(alpha = 0.14f))
                    .border(1.dp, Vela.Danger.copy(alpha = 0.4f), RoundedCornerShape(14.dp))
                    .padding(start = 14.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(err, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f).padding(vertical = 8.dp))
                TextButton(onClick = onDismissError) { Text("Скрыть", color = Vela.Muted) }
            }
        }

    }

    // ---- current server
    Column(Modifier.padding(top = 24.dp, bottom = 16.dp)) {
        CurrentServer(app, status, pings, onServers)
        Spacer(Modifier.height(12.dp))
        RoutingSwitch(app.settings.routing, onRouting)
    }
    }
    }
}

@Composable
private fun SpeedLabel(arrow: String, value: String) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(arrow, color = Vela.Muted)
        Spacer(Modifier.size(6.dp))
        Text(value, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun CurrentServer(app: AppState, status: VpnStatus, pings: Map<String, Int>, onClick: () -> Unit) {
    val profile = app.active
    val (badge, title, meta) = when {
        profile == null || profile.nodes.isEmpty() -> Triple("+", "Добавить сервер", "Ссылка на подписку или vless://, hy2://…")
        app.selectedNode == "auto" || profile.nodes.none { it.id == app.selectedNode } -> {
            val live = profile.nodes.firstOrNull { it.id == status.activeNode }
            if (live != null) {
                val ping = pings[live.id]?.takeIf { it > 0 }?.let { ", $it мс" } ?: ""
                Triple(badgeFor(live.name), "Автовыбор", "Сейчас: " + splitFlag(live.name).second + ping)
            } else {
                Triple("✦", "Автовыбор", "${profile.name}, серверов: ${profile.nodes.size}")
            }
        }
        else -> {
            val n = profile.nodes.first { it.id == app.selectedNode }
            val parts = listOf(n.protocol, n.security, profile.name).filter { it.isNotBlank() }.toMutableList()
            pings[n.id]?.takeIf { it > 0 }?.let { parts += "$it мс" }
            Triple(badgeFor(n.name), splitFlag(n.name).second, parts.joinToString(", "))
        }
    }
    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(22.dp))
            .background(Vela.Deep)
            .border(1.dp, Vela.Hull, RoundedCornerShape(22.dp))
            .clickable(onClick = onClick)
            .padding(14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Badge(badge, 42)
        Spacer(Modifier.size(14.dp))
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(meta, color = Vela.Muted, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Icon(Icons.AutoMirrored.Rounded.KeyboardArrowRight, null, tint = Vela.Muted)
    }
}

@Composable
fun Badge(text: String, size: Int) {
    Box(
        Modifier.size(size.dp).clip(RoundedCornerShape((size * 0.28).dp)).background(Vela.Hull),
        contentAlignment = Alignment.Center,
    ) {
        Text(text, color = Vela.Star, fontSize = (size * 0.45).sp)
    }
}

@Composable
private fun RoutingSwitch(value: String, onChange: (String) -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(22.dp))
            .background(Vela.Deep)
            .border(1.dp, Vela.Hull, RoundedCornerShape(22.dp))
            .padding(4.dp),
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        RoutingOption("Умный режим", "Российские сайты напрямую", value == "smart", Modifier.weight(1f)) { onChange("smart") }
        RoutingOption("Весь трафик", "Всё через сервер", value == "global", Modifier.weight(1f)) { onChange("global") }
    }
}

@Composable
private fun RoutingOption(title: String, sub: String, selected: Boolean, modifier: Modifier, onClick: () -> Unit) {
    Column(
        modifier
            .clip(RoundedCornerShape(18.dp))
            .background(if (selected) Vela.Hull else Color.Transparent)
            .clickable(role = Role.RadioButton, onClick = onClick)
            .padding(horizontal = 12.dp, vertical = 10.dp),
    ) {
        Text(title, fontWeight = FontWeight.SemiBold, fontSize = 14.sp, color = if (selected) Vela.Star else Vela.Text)
        Text(sub, color = Vela.Muted, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

private object Brush {
    fun topGlow() = androidx.compose.ui.graphics.Brush.verticalGradient(
        0f to Color(0xFF16254A), 0.45f to Vela.Ink, 1f to Vela.Ink,
    )
}
