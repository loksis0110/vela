package app.vela.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import app.vela.client.R
import app.vela.client.data.AppState
import app.vela.client.vpn.ConnState
import app.vela.client.vpn.VpnManager
import app.vela.client.vpn.VpnStatus
import app.vela.velacore.Velacore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray

/** One open connection of the running core. */
data class Conn(
    val id: String,
    val network: String,
    val host: String,
    val destination: String,
    val outbound: String,
    val rule: String,
    val process: String,
    val upload: Long,
    val download: Long,
    /** Unix milliseconds. */
    val start: Long,
)

private fun parseConnections(json: String): List<Conn> {
    val arr = JSONArray(json.ifBlank { "[]" })
    return (0 until arr.length()).mapNotNull { i ->
        arr.optJSONObject(i)?.let { o ->
            Conn(
                id = o.optString("id"), network = o.optString("network"), host = o.optString("host"),
                destination = o.optString("destination"), outbound = o.optString("outbound"), rule = o.optString("rule"),
                process = o.optString("process"), upload = o.optLong("upload"), download = o.optLong("download"),
                start = o.optLong("start"),
            )
        }
    }
}

@Composable
fun ConnectionsScreen(app: AppState, status: VpnStatus, onBack: () -> Unit) {
    val connected = status.state == ConnState.Connected
    val scope = rememberCoroutineScope()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    var list by remember { mutableStateOf<List<Conn>?>(null) }
    var failed by remember { mutableStateOf(false) }
    var query by rememberSaveable { mutableStateOf("") }
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    var reload by remember { mutableIntStateOf(0) }

    // node-<id> → server name, for the servers of the running profile
    val profile = app.active
    val names = remember(profile) {
        profile?.nodes?.associate { n -> runCatching { Velacore.nodeTag(n.id) }.getOrDefault("node-" + n.id) to n.name }.orEmpty()
    }
    val direct = stringResource(R.string.conn_direct)
    val blocked = stringResource(R.string.conn_blocked)
    fun outboundName(tag: String): String = when {
        tag.isBlank() -> ""
        names.containsKey(tag) -> names.getValue(tag)
        tag == "direct" -> direct
        tag == "block" || tag == "reject" -> blocked
        tag.startsWith("dns") -> "DNS"
        tag == "auto" || tag == "proxy" -> "VPN"
        else -> tag
    }

    // asks the core every 1.5 s while the screen is visible
    LaunchedEffect(connected, reload) {
        if (!connected) {
            list = emptyList()
            return@LaunchedEffect
        }
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                val port = VpnManager.controllerPort
                val secret = VpnManager.controllerSecret
                val result = withContext(Dispatchers.IO) {
                    runCatching { parseConnections(Velacore.listConnections(port, secret)) }
                }
                result.onSuccess { list = it; failed = false }.onFailure { failed = true; if (list == null) list = emptyList() }
                now = System.currentTimeMillis()
                delay(1500)
            }
        }
    }

    fun close(id: String) {
        list = if (id.isEmpty()) emptyList() else list?.filterNot { it.id == id }
        scope.launch {
            withContext(Dispatchers.IO) {
                runCatching { Velacore.closeConnection(VpnManager.controllerPort, VpnManager.controllerSecret, id) }
            }
            reload++
        }
    }

    Column(Modifier.fillMaxSize().background(Vela.Ink).safeDrawingPadding()) {
        val all = list
        Row(Modifier.fillMaxWidth().padding(start = 4.dp, end = 8.dp, top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, stringResource(R.string.back)) }
            Text(stringResource(R.string.connections_title), style = MaterialTheme.typography.titleLarge)
            if (!all.isNullOrEmpty()) Text("  " + all.size, color = Vela.Muted, style = MaterialTheme.typography.titleLarge)
            Box(Modifier.weight(1f))
            if (connected && !all.isNullOrEmpty()) {
                TextButton(onClick = { close("") }) { Text(stringResource(R.string.conn_close_all), color = Vela.Danger) }
            }
        }

        if (!connected) {
            Message(stringResource(R.string.conn_not_connected), stringResource(R.string.conn_not_connected_hint))
            return@Column
        }

        OutlinedTextField(
            value = query, onValueChange = { query = it }, singleLine = true,
            leadingIcon = { Icon(Icons.Rounded.Search, null, tint = Vela.Muted) },
            trailingIcon = {
                if (query.isNotEmpty()) IconButton(onClick = { query = "" }) { Icon(Icons.Rounded.Close, stringResource(R.string.clear), tint = Vela.Muted) }
            },
            placeholder = { Text(stringResource(R.string.conn_search), color = Vela.Muted) },
            shape = RoundedCornerShape(14.dp),
            colors = OutlinedTextFieldDefaults.colors(
                focusedBorderColor = Vela.Line, unfocusedBorderColor = Vela.Hull, cursorColor = Vela.Star,
                focusedContainerColor = Vela.Deep, unfocusedContainerColor = Vela.Deep,
            ),
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
        )
        if (failed) {
            Text(
                stringResource(R.string.conn_failed), color = Vela.Danger, fontSize = 12.sp,
                modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp),
            )
        }

        if (all == null) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator(color = Vela.Muted) }
            return@Column
        }
        val q = query.trim().lowercase()
        val shown = if (q.isEmpty()) all else all.filter { c ->
            listOf(c.host, c.destination, c.process, c.rule, outboundName(c.outbound), c.network).any { q in it.lowercase() }
        }
        if (shown.isEmpty()) {
            Message(
                stringResource(if (all.isEmpty()) R.string.conn_empty else R.string.nothing_found),
                if (all.isEmpty()) stringResource(R.string.conn_empty_hint) else "",
            )
            return@Column
        }
        LazyColumn(contentPadding = PaddingValues(start = 10.dp, end = 10.dp, top = 4.dp, bottom = 24.dp)) {
            items(shown, key = { it.id }) { c -> ConnRow(c, outboundName(c.outbound), now) { close(c.id) } }
        }
    }
}

@Composable
private fun Message(title: String, text: String) {
    Column(
        Modifier.fillMaxWidth().padding(16.dp).clip(RoundedCornerShape(22.dp))
            .border(1.dp, Vela.Line, RoundedCornerShape(22.dp)).padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(title, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
        if (text.isNotEmpty()) Text(text, color = Vela.Muted, textAlign = TextAlign.Center, modifier = Modifier.padding(top = 6.dp))
    }
}

@Composable
private fun ConnRow(c: Conn, server: String, now: Long, onClose: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().padding(vertical = 3.dp).clip(RoundedCornerShape(14.dp)).background(Vela.Deep)
            .border(1.dp, Vela.Hull, RoundedCornerShape(14.dp)).padding(start = 12.dp, top = 9.dp, bottom = 9.dp, end = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(c.host.ifBlank { c.destination }, fontWeight = FontWeight.SemiBold, fontSize = 14.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
            val viaDirect = c.outbound == "direct"
            Text(
                server, color = if (viaDirect) Vela.Muted else Vela.Sea, fontSize = 12.sp,
                maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
            val meta = listOf(
                c.network.uppercase(),
                if (c.start > 0) formatDuration((now - c.start) / 1000) else "",
                c.rule,
                c.process,
            ).filter { it.isNotBlank() }.joinToString(" · ")
            Text(meta, color = Vela.Muted, fontSize = 11.5.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Column(horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.Center, modifier = Modifier.padding(start = 8.dp)) {
            Text("↓ " + formatBytes(c.download), fontSize = 12.sp, maxLines = 1)
            Text("↑ " + formatBytes(c.upload), color = Vela.Muted, fontSize = 12.sp, maxLines = 1)
        }
        IconButton(onClick = onClose, modifier = Modifier.size(40.dp)) {
            Icon(Icons.Rounded.Close, stringResource(R.string.conn_close), tint = Vela.Muted, modifier = Modifier.size(18.dp))
        }
    }
}
