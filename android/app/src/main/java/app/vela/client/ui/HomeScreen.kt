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
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.fillMaxHeight
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
import androidx.compose.material.icons.outlined.BarChart
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.rounded.Campaign
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.SwapVert
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
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
import app.vela.client.vpn.IpInfo
import app.vela.client.vpn.SpeedState
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
    onDismissNotice: () -> Unit,
    history: List<Pair<Long, Long>>,
    ip: IpInfo?,
    ipBusy: Boolean,
    ipError: String?,
    speed: SpeedState,
    onIp: () -> Unit,
    onSpeed: () -> Unit,
    announce: String?,
    onDismissAnnounce: () -> Unit,
    onStats: () -> Unit,
    onConnections: () -> Unit,
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
            IconButton(onClick = onStats) {
                Icon(Icons.Outlined.BarChart, contentDescription = stringResource(R.string.stats_title), tint = Vela.Muted)
            }
            IconButton(onClick = onSettings) {
                Icon(Icons.Outlined.Settings, contentDescription = stringResource(R.string.settings_title), tint = Vela.Muted)
            }
        }

        // ---- constellation control
        val label = stringResource(
            when (status.state) {
                ConnState.Connected -> R.string.disconnect
                ConnState.Connecting -> R.string.cancel
                ConnState.Disconnecting -> R.string.disconnecting_short
                ConnState.Disconnected -> R.string.connect
            },
        )
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

        val (statusRes, statusColor) = when (status.state) {
            ConnState.Connected -> R.string.status_protected to Vela.Sea
            ConnState.Connecting -> R.string.status_connecting to Vela.Flare
            ConnState.Disconnecting -> R.string.status_disconnecting to Vela.Text
            ConnState.Disconnected -> R.string.status_off to Vela.Text
        }
        Text(stringResource(statusRes), style = MaterialTheme.typography.headlineSmall, color = statusColor, modifier = Modifier.padding(top = 10.dp))
        val sub = when {
            status.state == ConnState.Connected && status.since > 0 ->
                stringResource(R.string.home_session, formatDuration(now - status.since), formatBytes(status.upTotal + status.downTotal))
            app.profiles.isEmpty() -> stringResource(R.string.home_add_server_hint)
            else -> ""
        }
        Text(sub, color = Vela.Muted, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 4.dp).height(20.dp))
        if (status.state == ConnState.Connected) {
            Row(Modifier.padding(top = 10.dp), horizontalArrangement = Arrangement.spacedBy(28.dp)) {
                SpeedLabel("↓", formatSpeed(status.down))
                SpeedLabel("↑", formatSpeed(status.up))
            }
            TrafficGraph(history, Modifier.padding(top = 12.dp).fillMaxWidth())
            TextButton(onClick = onConnections, modifier = Modifier.padding(top = 2.dp)) {
                Icon(Icons.Rounded.SwapVert, null, tint = Vela.Muted, modifier = Modifier.size(16.dp))
                Spacer(Modifier.size(6.dp))
                Text(stringResource(R.string.connections_title), color = Vela.Muted, fontSize = 13.sp)
            }
        }

        announce?.let { AnnounceBanner(it, onDismissAnnounce, Modifier.padding(top = 16.dp)) }

        status.notice?.let { msg ->
            Row(
                Modifier
                    .padding(top = 16.dp)
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(14.dp))
                    .background(Vela.Sea.copy(alpha = 0.12f))
                    .border(1.dp, Vela.Sea.copy(alpha = 0.4f), RoundedCornerShape(14.dp))
                    .padding(start = 14.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(msg, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f).padding(vertical = 8.dp))
                TextButton(onClick = onDismissNotice) { Text(stringResource(R.string.ok_got_it), color = Vela.Muted) }
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
                TextButton(onClick = onDismissError) { Text(stringResource(R.string.hide), color = Vela.Muted) }
            }
        }

    }

    // ---- current server
    Column(Modifier.padding(top = 24.dp, bottom = 16.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            val (ipValue, ipMeta) = ipTexts(ip, ipBusy, ipError)
            ToolCard(stringResource(R.string.tool_ip), ipValue, ipMeta, ipBusy, Modifier.weight(1f), onIp)
            val (spValue, spMeta) = speedTexts(speed)
            ToolCard(stringResource(R.string.tool_speed), spValue, spMeta, speed.running, Modifier.weight(1f), onSpeed)
        }
        Spacer(Modifier.height(10.dp))
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
    val auto = stringResource(R.string.auto_select)
    val (badge, title, meta) = when {
        profile == null || profile.nodes.isEmpty() ->
            Triple("+", stringResource(R.string.home_add_server), stringResource(R.string.home_add_server_meta))
        app.selectedNode == "auto" || profile.nodes.none { it.id == app.selectedNode } -> {
            val live = profile.nodes.firstOrNull { it.id == status.activeNode }
            if (live != null) {
                val ping = pings[live.id]?.takeIf { it > 0 }?.let { ", " + formatMs(it) } ?: ""
                Triple(badgeFor(live.name), auto, stringResource(R.string.now_using, splitFlag(live.name).second) + ping)
            } else {
                Triple("✦", auto, stringResource(R.string.home_auto_meta, profile.name, profile.nodes.size))
            }
        }
        else -> {
            val n = profile.nodes.first { it.id == app.selectedNode }
            val parts = listOf(n.protocol, if (n.engine == "xray") "Xray" else "", n.security, profile.name)
                .filter { it.isNotBlank() }.toMutableList()
            pings[n.id]?.takeIf { it > 0 }?.let { parts += formatMs(it) }
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

/** Routing modes: value, short title for the quick switch, full title, one-line explanation. */
data class RoutingMode(val value: String, val short: Int, val title: Int, val hint: Int)

val routingModes = listOf(
    RoutingMode("smart", R.string.routing_smart_short, R.string.routing_smart, R.string.routing_smart_hint),
    RoutingMode("blocked", R.string.routing_blocked_short, R.string.routing_blocked, R.string.routing_blocked_hint),
    RoutingMode("global", R.string.routing_global_short, R.string.routing_global, R.string.routing_global_hint),
)

@Composable
private fun RoutingSwitch(value: String, onChange: (String) -> Unit) {
    val current = routingModes.firstOrNull { it.value == value } ?: routingModes[0]
    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(22.dp))
            .background(Vela.Deep)
            .border(1.dp, Vela.Hull, RoundedCornerShape(22.dp))
            .padding(4.dp),
    ) {
        Row(Modifier.fillMaxWidth().height(IntrinsicSize.Min), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            routingModes.forEach { m ->
                RoutingOption(stringResource(m.short), m.value == current.value, Modifier.weight(1f).fillMaxHeight()) { onChange(m.value) }
            }
        }
        Text(
            stringResource(current.hint), color = Vela.Muted, fontSize = 12.sp, textAlign = TextAlign.Center,
            modifier = Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 7.dp),
        )
    }
}

@Composable
private fun RoutingOption(title: String, selected: Boolean, modifier: Modifier, onClick: () -> Unit) {
    Box(
        modifier
            .clip(RoundedCornerShape(18.dp))
            .background(if (selected) Vela.Hull else Color.Transparent)
            .clickable(role = Role.RadioButton, onClick = onClick)
            .padding(horizontal = 6.dp, vertical = 10.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            title, fontWeight = FontWeight.SemiBold, fontSize = 13.5.sp, lineHeight = 17.sp, textAlign = TextAlign.Center,
            color = if (selected) Vela.Star else Vela.Text, maxLines = 2, overflow = TextOverflow.Ellipsis,
        )
    }
}

/** Message from the subscription provider; hidden until the text changes. */
@Composable
fun AnnounceBanner(text: String, onDismiss: () -> Unit, modifier: Modifier = Modifier) {
    var expanded by remember(text) { mutableStateOf(false) }
    Row(
        modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(14.dp))
            .background(Vela.Flare.copy(alpha = 0.10f))
            .border(1.dp, Vela.Flare.copy(alpha = 0.35f), RoundedCornerShape(14.dp))
            .clickable { expanded = !expanded }
            .padding(start = 12.dp, top = 4.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Rounded.Campaign, null, tint = Vela.Flare, modifier = Modifier.size(20.dp))
        Column(Modifier.weight(1f).padding(start = 10.dp, top = 6.dp, bottom = 6.dp)) {
            Text(stringResource(R.string.announce_from_provider), color = Vela.Flare, fontSize = 11.5.sp, fontWeight = FontWeight.SemiBold)
            Text(
                text, style = MaterialTheme.typography.bodyMedium,
                maxLines = if (expanded) 20 else 3, overflow = TextOverflow.Ellipsis,
            )
        }
        IconButton(onClick = onDismiss, modifier = Modifier.size(40.dp)) {
            Icon(Icons.Rounded.Close, stringResource(R.string.hide), tint = Vela.Muted, modifier = Modifier.size(18.dp))
        }
    }
}

private object Brush {
    fun topGlow() = androidx.compose.ui.graphics.Brush.verticalGradient(
        0f to Vela.Glow, 0.45f to Vela.Ink, 1f to Vela.Ink,
    )
}
