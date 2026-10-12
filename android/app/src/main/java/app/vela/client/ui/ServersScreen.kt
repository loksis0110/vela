package app.vela.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.QrCode2
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Speed
import androidx.compose.material.icons.rounded.Stop
import androidx.compose.material.icons.rounded.Star
import androidx.compose.material.icons.rounded.StarBorder
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import app.vela.client.R
import app.vela.client.data.AppState
import app.vela.client.data.Store
import app.vela.client.data.Node
import app.vela.client.data.Profile
import app.vela.client.vpn.ConnState
import app.vela.client.vpn.VpnStatus

@Composable
fun ServersScreen(
    app: AppState,
    status: VpnStatus,
    pings: Map<String, Int>,
    reasons: Map<String, String>,
    probe: Pair<Int, Int>?,
    onBack: () -> Unit,
    onAdd: () -> Unit,
    onPing: (profileId: String) -> Unit,
    onSelect: (profileId: String, nodeId: String) -> Unit,
    onRefresh: (Profile) -> Unit,
    onRefreshAll: () -> Unit,
    onRename: (Profile, String) -> Unit,
    onDeleteProfile: (Profile) -> Unit,
    onDeleteNode: (Profile, Node) -> Unit,
    onOpenUrl: (String) -> Unit,
    refreshing: Boolean,
    onFavorite: (nodeId: String, on: Boolean) -> Unit,
    onShare: (title: String, text: String) -> Unit,
    onStopProbe: () -> Unit,
    onPingOne: (profileId: String, nodeId: String) -> Unit,
    onSort: (Boolean) -> Unit,
) {
    var viewId by rememberSaveable { mutableStateOf(app.active?.id ?: "") }
    val profile = app.profiles.firstOrNull { it.id == viewId } ?: app.active
    var confirmDelete by remember { mutableStateOf<Profile?>(null) }
    var renaming by remember { mutableStateOf<Profile?>(null) }
    val fastestFirst = app.sortByPing
    var query by rememberSaveable { mutableStateOf("") }
    var onlyFav by rememberSaveable { mutableStateOf(false) }
    var onlyOk by rememberSaveable { mutableStateOf(false) }
    var proto by rememberSaveable { mutableStateOf("") }
    var country by rememberSaveable { mutableStateOf("") }
    var protoMenu by remember { mutableStateOf(false) }
    var countryMenu by remember { mutableStateOf(false) }

    /** Another subscription may have no such country or protocol: its filters start empty. */
    fun show(id: String) {
        if (id == viewId) return
        viewId = id
        proto = ""
        country = ""
    }

    fun matches(n: Node): Boolean {
        val q = query.trim().lowercase()
        if (q.isNotEmpty()) {
            val flag = splitFlag(n.name).first
            val hay = listOf(n.name, n.protocol, n.security, n.network, n.server, flag?.let { countryName(it) } ?: "").joinToString(" ").lowercase()
            if (q.split(' ').filter { it.isNotBlank() }.any { it !in hay }) return false
        }
        if (onlyFav && n.id !in app.favorites) return false
        if (onlyOk && (pings[n.id] ?: 0) <= 0) return false
        if (proto.isNotEmpty() && n.protocol != proto) return false
        if (country.isNotEmpty() && splitFlag(n.name).first != country) return false
        return true
    }

    Column(Modifier.fillMaxSize().background(Vela.Ink).safeDrawingPadding()) {
        Row(Modifier.fillMaxWidth().padding(start = 4.dp, end = 12.dp, top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, stringResource(R.string.back)) }
            Text(stringResource(R.string.servers_title), style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f))
            if (app.profiles.any { it.kind == "subscription" }) {
                IconButton(
                    onClick = { if (profile?.kind == "subscription") onRefresh(profile) else onRefreshAll() },
                    enabled = !refreshing,
                ) {
                    if (refreshing) CircularProgressIndicator(Modifier.size(18.dp), color = Vela.Muted, strokeWidth = 2.dp)
                    else Icon(Icons.Rounded.Refresh, stringResource(R.string.refresh_subscription), tint = Vela.Muted)
                }
            }
            if (probe != null) {
                IconButton(onClick = onStopProbe) { Icon(Icons.Rounded.Stop, stringResource(R.string.probe_stop_desc), tint = Vela.Danger) }
            } else {
                IconButton(onClick = { profile?.let { onPing(it.id) } }, enabled = profile != null) {
                    Icon(Icons.Rounded.Speed, stringResource(R.string.probe_all_desc), tint = Vela.Muted)
                }
            }
            Button(
                onClick = onAdd,
                shape = RoundedCornerShape(10.dp),
                contentPadding = PaddingValues(horizontal = 14.dp),
                modifier = Modifier.height(36.dp),
            ) { Text(stringResource(R.string.add), fontSize = 14.sp) }
        }

        if (app.profiles.isNotEmpty()) {
            LazyRow(
                contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                items(app.profiles, key = { it.id }) { p ->
                    val sel = p.id == profile?.id
                    Text(
                        (if (p.id == app.activeProfile) "● " else "") + p.name + "  " + p.nodes.size,
                        fontSize = 14.sp,
                        color = if (sel) Vela.Text else Vela.Muted,
                        modifier = Modifier
                            .clip(RoundedCornerShape(50))
                            .background(if (sel) Vela.Hull else Color.Transparent)
                            .border(1.dp, if (sel) Vela.Line else Vela.Hull, RoundedCornerShape(50))
                            .clickable { show(p.id) }
                            .padding(horizontal = 14.dp, vertical = 7.dp),
                    )
                }
                item {
                    Text(
                        stringResource(R.string.add_subscription_chip),
                        fontSize = 14.sp,
                        color = Vela.Star,
                        modifier = Modifier
                            .clip(RoundedCornerShape(50))
                            .border(1.dp, Vela.Line, RoundedCornerShape(50))
                            .clickable(onClick = onAdd)
                            .padding(horizontal = 14.dp, vertical = 7.dp),
                    )
                }
            }
        }
        probe?.let { (done, total) ->
            Column(Modifier.padding(horizontal = 16.dp, vertical = 4.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(stringResource(R.string.probe_progress, done, total), color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.weight(1f))
                    TextButton(onClick = onStopProbe) { Text(stringResource(R.string.stop), color = Vela.Star, fontSize = 13.sp) }
                }
                LinearProgressIndicator(
                    progress = { if (total == 0) 0f else done.toFloat() / total },
                    color = Vela.Star, trackColor = Vela.Hull, drawStopIndicator = {},
                    modifier = Modifier.fillMaxWidth().padding(top = 6.dp).height(4.dp).clip(RoundedCornerShape(4.dp)),
                )
            }
        }

        if (profile != null && profile.nodes.isNotEmpty()) {
            OutlinedTextField(
                value = query, onValueChange = { query = it }, singleLine = true,
                leadingIcon = { Icon(Icons.Rounded.Search, null, tint = Vela.Muted) },
                trailingIcon = {
                    if (query.isNotEmpty()) IconButton(onClick = { query = "" }) { Icon(Icons.Rounded.Close, stringResource(R.string.clear), tint = Vela.Muted) }
                },
                placeholder = { Text(stringResource(R.string.servers_search), color = Vela.Muted) },
                shape = RoundedCornerShape(14.dp),
                colors = OutlinedTextFieldDefaults.colors(
                    focusedBorderColor = Vela.Line, unfocusedBorderColor = Vela.Hull, cursorColor = Vela.Star,
                    focusedContainerColor = Vela.Deep, unfocusedContainerColor = Vela.Deep,
                ),
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
            )
            val protocols = remember(profile.nodes) { profile.nodes.map { it.protocol }.filter { it.isNotBlank() }.distinct().sorted() }
            val flags = remember(profile.nodes) { profile.nodes.mapNotNull { splitFlag(it.name).first }.distinct().sortedBy { countryName(it) } }
            ChipRow {
                Chip(stringResource(R.string.filter_favorites), onlyFav) { onlyFav = !onlyFav }
                Chip(stringResource(R.string.filter_working), onlyOk) { onlyOk = !onlyOk }
                if (protocols.size > 1 || proto.isNotEmpty()) {
                    val allProtocols = stringResource(R.string.filter_all_protocols)
                    Box {
                        Chip(proto.ifEmpty { allProtocols } + " ▾", proto.isNotEmpty()) { protoMenu = true }
                        DropdownMenu(protoMenu, { protoMenu = false }, containerColor = Vela.Deep) {
                            DropdownMenuItem({ Text(allProtocols) }, { proto = ""; protoMenu = false })
                            protocols.forEach { p -> DropdownMenuItem({ Text(p) }, { proto = p; protoMenu = false }) }
                        }
                    }
                }
                if (flags.size > 1 || country.isNotEmpty()) {
                    Box {
                        val allCountries = stringResource(R.string.filter_all_countries)
                        Chip(if (country.isEmpty()) "$allCountries ▾" else "$country ${countryName(country)} ▾", country.isNotEmpty()) { countryMenu = true }
                        DropdownMenu(countryMenu, { countryMenu = false }, containerColor = Vela.Deep) {
                            DropdownMenuItem({ Text(allCountries) }, { country = ""; countryMenu = false })
                            flags.forEach { f ->
                                DropdownMenuItem({ Text("$f ${countryName(f)}  ${profile.nodes.count { splitFlag(it.name).first == f }}") }, { country = f; countryMenu = false })
                            }
                        }
                    }
                }
                Chip(stringResource(R.string.filter_fastest), fastestFirst) { onSort(!fastestFirst) }
            }
        }

        if (profile == null) {
            Column(
                Modifier.fillMaxWidth().padding(16.dp).clip(RoundedCornerShape(22.dp))
                    .border(1.dp, Vela.Line, RoundedCornerShape(22.dp)).padding(24.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Text(stringResource(R.string.servers_empty), style = MaterialTheme.typography.titleMedium)
                Text(
                    stringResource(R.string.servers_empty_hint),
                    color = Vela.Muted, textAlign = TextAlign.Center, modifier = Modifier.padding(top = 6.dp),
                )
                Button(onClick = onAdd, modifier = Modifier.padding(top = 14.dp)) { Text(stringResource(R.string.add_servers)) }
            }
            return@Column
        }

        val isActive = profile.id == app.activeProfile
        val autoSelected = isActive && (app.selectedNode == "auto" || profile.nodes.none { it.id == app.selectedNode })
        val connected = status.state == ConnState.Connected && isActive

        val dismissed by Store.dismissedAnnounce.collectAsState()
        val announce = Store.visibleAnnounce(profile, dismissed)
        val autoTitle = stringResource(R.string.auto_select)
        val autoLive = status.activeNode?.let { id -> profile.nodes.firstOrNull { it.id == id } }
            ?.let { stringResource(R.string.now_using, splitFlag(it.name).second) }
        val autoMeta = stringResource(R.string.auto_select_meta)
        val onlyFavorites = stringResource(R.string.auto_only_favorites)
        // hundreds of servers: filtered and sorted only when something they depend on changes,
        // not on every second of the traffic counter
        val visible = remember(profile.nodes, query, onlyFav, onlyOk, proto, country, app.favorites, if (onlyOk) pings else null) {
            profile.nodes.filter { matches(it) }
        }
        val ordered = remember(visible, fastestFirst, app.favorites, if (fastestFirst) pings else null) {
            if (fastestFirst) {
                visible.sortedBy { n ->
                    val ms = pings[n.id]
                    when {
                        ms == null || ms == 0 -> 1_000_000
                        ms < 0 -> 10_000_000
                        else -> ms
                    }
                }
            } else {
                visible.sortedByDescending { it.id in app.favorites }
            }
        }
        LazyColumn(contentPadding = PaddingValues(start = 10.dp, end = 10.dp, bottom = 24.dp)) {
            if (announce != null) {
                item(key = "announce") {
                    AnnounceBanner(announce, { Store.dismissAnnounce(profile.id, announce) }, Modifier.padding(horizontal = 6.dp, vertical = 4.dp))
                }
            }
            if (profile.kind == "subscription") {
                item {
                    SubscriptionCard(
                        profile, refreshing, app.profiles.count { it.kind == "subscription" } > 1,
                        onRefresh = { onRefresh(profile) }, onRefreshAll = onRefreshAll,
                        onRename = { renaming = profile }, onDelete = { confirmDelete = profile }, onOpenUrl = onOpenUrl,
                        onShare = { onShare(profile.name, profile.url) },
                    )
                }
            }
            item {
                val live = if (autoSelected && connected) autoLive else null
                NodeRow(
                    badge = "✦", title = autoTitle,
                    meta = live ?: autoMeta,
                    ping = null, selected = autoSelected, inUse = false, reason = null,
                    onClick = { onSelect(profile.id, "auto") }, onDelete = null,
                    meta2 = if (app.favorites.any { f -> profile.nodes.any { it.id == f } }) onlyFavorites else null,
                )
            }
            if (visible.size != profile.nodes.size) {
                item {
                    Text(
                        stringResource(R.string.servers_shown, visible.size, profile.nodes.size), color = Vela.Muted, fontSize = 12.sp,
                        modifier = Modifier.padding(horizontal = 10.dp, vertical = 2.dp),
                    )
                }
            }
            if (visible.isEmpty()) {
                item {
                    val elsewhere = if (query.isNotBlank()) app.profiles.filter { it.id != profile.id && it.nodes.any { n -> matches(n) } } else emptyList()
                    Column(
                        Modifier.fillMaxWidth().padding(8.dp).clip(RoundedCornerShape(18.dp))
                            .border(1.dp, Vela.Line, RoundedCornerShape(18.dp)).padding(20.dp),
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        Text(stringResource(R.string.nothing_found), style = MaterialTheme.typography.titleMedium)
                        Text(
                            stringResource(if (elsewhere.isEmpty()) R.string.nothing_found_hint else R.string.found_elsewhere),
                            color = Vela.Muted, textAlign = TextAlign.Center, modifier = Modifier.padding(top = 4.dp),
                        )
                        elsewhere.forEach { o ->
                            TextButton(onClick = { show(o.id) }) { Text("${o.name} · ${o.nodes.count { matches(it) }}") }
                        }
                    }
                }
            }
            items(ordered, key = { it.id }) { n ->
                val tags = listOf(n.protocol, n.security, if (n.network.isNotBlank() && n.network != "tcp") n.network.uppercase() else "")
                    .filter { it.isNotBlank() }.joinToString(" ")
                NodeRow(
                    badge = badgeFor(n.name), title = splitFlag(n.name).second, meta = tags,
                    ping = pingLabel(pings[n.id]), selected = isActive && app.selectedNode == n.id,
                    reason = if ((pings[n.id] ?: 0) < 0) reasons[n.id] else null,
                    inUse = connected && status.activeNode == n.id,
                    onClick = { onSelect(profile.id, n.id) },
                    onDelete = if (profile.kind == "manual") ({ onDeleteNode(profile, n) }) else null,
                    favorite = n.id in app.favorites,
                    xray = n.engine == "xray",
                    onFavorite = { onFavorite(n.id, n.id !in app.favorites) },
                    onShare = { onShare(splitFlag(n.name).second, n.raw.optString("link")) },
                    onPing = { onPingOne(profile.id, n.id) },
                )
            }
        }
    }

    renaming?.let { p ->
        var text by remember(p.id) { mutableStateOf(p.customName.ifEmpty { p.name }) }
        AlertDialog(
            onDismissRequest = { renaming = null },
            title = { Text(stringResource(R.string.rename_title)) },
            text = {
                Column {
                    Text(stringResource(R.string.rename_hint), color = Vela.Muted, fontSize = 13.sp)
                    OutlinedTextField(
                        value = text, onValueChange = { text = it.take(60) }, singleLine = true,
                        colors = OutlinedTextFieldDefaults.colors(focusedBorderColor = Vela.Star, unfocusedBorderColor = Vela.Line, cursorColor = Vela.Star),
                        modifier = Modifier.fillMaxWidth().padding(top = 10.dp),
                    )
                }
            },
            confirmButton = { TextButton(onClick = { onRename(p, text); renaming = null }) { Text(stringResource(R.string.save)) } },
            dismissButton = { TextButton(onClick = { renaming = null }) { Text(stringResource(R.string.cancel), color = Vela.Muted) } },
            containerColor = Vela.Deep,
        )
    }

    confirmDelete?.let { p ->
        AlertDialog(
            onDismissRequest = { confirmDelete = null },
            title = { Text(stringResource(if (p.kind == "manual") R.string.delete_servers_title else R.string.delete_sub_title)) },
            text = { Text(stringResource(R.string.delete_profile_text, p.name)) },
            confirmButton = {
                TextButton(onClick = { confirmDelete = null; onDeleteProfile(p) }) { Text(stringResource(R.string.delete), color = Vela.Danger) }
            },
            dismissButton = { TextButton(onClick = { confirmDelete = null }) { Text(stringResource(R.string.cancel)) } },
            containerColor = Vela.Deep,
        )
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun SubscriptionCard(
    p: Profile,
    refreshing: Boolean,
    several: Boolean,
    onRefresh: () -> Unit,
    onRefreshAll: () -> Unit,
    onRename: () -> Unit,
    onDelete: () -> Unit,
    onOpenUrl: (String) -> Unit,
    onShare: () -> Unit,
) {
    Column(
        Modifier.fillMaxWidth().padding(horizontal = 6.dp, vertical = 6.dp)
            .clip(RoundedCornerShape(14.dp)).background(Vela.Deep)
            .border(1.dp, Vela.Hull, RoundedCornerShape(14.dp)).padding(14.dp),
    ) {
        val used = p.upload + p.download
        if (p.total > 0) {
            InfoRow(stringResource(R.string.sub_traffic), stringResource(R.string.sub_traffic_of, formatBytes(used), formatBytes(p.total)))
            val frac = (used.toFloat() / p.total).coerceIn(0f, 1f)
            LinearProgressIndicator(
                progress = { frac },
                color = if (frac > 0.85f) Vela.Flare else Vela.Star,
                trackColor = Vela.Hull,
                drawStopIndicator = {},
                modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp).height(4.dp).clip(RoundedCornerShape(4.dp)),
            )
        } else if (used > 0) {
            InfoRow(stringResource(R.string.sub_traffic), stringResource(R.string.sub_traffic_unlimited, formatBytes(used)))
        }
        if (p.expire > 0) {
            val days = ((p.expire * 1000 - System.currentTimeMillis()) / 86_400_000L).toInt()
            val expired = p.expire * 1000 <= System.currentTimeMillis()
            InfoRow(
                stringResource(if (expired) R.string.sub_expired else R.string.sub_valid_until),
                formatDate(p.expire) + if (!expired && days in 0..7) " " + stringResource(R.string.sub_days_left, days) else "",
            )
        }
        if (p.updatedAt > 0) InfoRow(stringResource(R.string.sub_updated), formatDateTime(p.updatedAt))
        FlowRow(Modifier.padding(top = 6.dp)) {
            TextButton(onClick = onRefresh, enabled = !refreshing) {
                Text(stringResource(if (refreshing) R.string.refreshing else R.string.refresh), color = Vela.Muted)
            }
            if (several) TextButton(onClick = onRefreshAll, enabled = !refreshing) { Text(stringResource(R.string.refresh_all), color = Vela.Muted) }
            TextButton(onClick = onRename) { Text(stringResource(R.string.rename), color = Vela.Muted) }
            TextButton(onClick = onShare) { Text(stringResource(R.string.share), color = Vela.Muted) }
            if (p.supportUrl.isNotBlank()) TextButton(onClick = { onOpenUrl(p.supportUrl) }) { Text(stringResource(R.string.support), color = Vela.Muted) }
            TextButton(onClick = onDelete) { Text(stringResource(R.string.delete), color = Vela.Muted) }
        }
    }
}

@Composable
private fun InfoRow(label: String, value: String) {
    Row(Modifier.fillMaxWidth().padding(vertical = 1.dp)) {
        Text(label, color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.weight(1f))
        Text(value, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun NodeRow(
    badge: String,
    title: String,
    meta: String,
    ping: PingLabel?,
    selected: Boolean,
    inUse: Boolean,
    reason: String?,
    onClick: () -> Unit,
    onDelete: (() -> Unit)?,
    favorite: Boolean = false,
    onFavorite: (() -> Unit)? = null,
    onShare: (() -> Unit)? = null,
    meta2: String? = null,
    onPing: (() -> Unit)? = null,
    xray: Boolean = false,
) {
    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(14.dp))
            .background(if (selected) Vela.Deep else Color.Transparent)
            .then(if (selected) Modifier.border(1.dp, Vela.Line, RoundedCornerShape(14.dp)) else Modifier)
            .clickable(role = Role.RadioButton, onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Badge(badge, 36)
        Spacer(Modifier.size(12.dp))
        Column(Modifier.weight(1f)) {
            Text(title, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(meta, color = Vela.Muted, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, false))
                if (xray) XrayBadge()
                if (inUse) Text("  ● " + stringResource(R.string.in_use), color = Vela.Sea, fontSize = 12.sp, maxLines = 1)
                if (meta2 != null) Text("  ★ $meta2", color = Vela.Flare, fontSize = 12.sp, maxLines = 1)
            }
            if (!reason.isNullOrBlank()) Text(reason, color = Vela.Danger, fontSize = 12.sp, maxLines = 2)
        }
        // tap the delay (or the gauge) to check only this server
        when {
            ping != null && onPing != null -> Text(
                ping.text, color = ping.color, fontSize = 13.sp,
                modifier = Modifier.padding(start = 4.dp).clip(RoundedCornerShape(8.dp))
                    .clickable(onClickLabel = stringResource(R.string.check_server), onClick = onPing).padding(horizontal = 6.dp, vertical = 6.dp),
            )
            ping != null -> Text(ping.text, color = ping.color, fontSize = 13.sp, modifier = Modifier.padding(start = 8.dp))
            onPing != null -> IconButton(onClick = onPing, modifier = Modifier.size(32.dp)) {
                Icon(Icons.Rounded.Speed, stringResource(R.string.check_server), tint = Vela.Line, modifier = Modifier.size(18.dp))
            }
        }
        if (onFavorite != null) {
            IconButton(onClick = onFavorite, modifier = Modifier.size(36.dp)) {
                Icon(
                    if (favorite) Icons.Rounded.Star else Icons.Rounded.StarBorder,
                    stringResource(if (favorite) R.string.favorite_remove else R.string.favorite_add),
                    tint = if (favorite) Vela.Flare else Vela.Line, modifier = Modifier.size(20.dp),
                )
            }
        }
        if (onShare != null) {
            IconButton(onClick = onShare, modifier = Modifier.size(32.dp)) {
                Icon(Icons.Rounded.QrCode2, stringResource(R.string.share), tint = Vela.Line, modifier = Modifier.size(18.dp))
            }
        }
        if (onDelete != null) {
            IconButton(onClick = onDelete, modifier = Modifier.size(36.dp)) {
                Icon(Icons.Rounded.Close, stringResource(R.string.delete_server), tint = Vela.Muted, modifier = Modifier.size(18.dp))
            }
        }
        if (selected) Icon(Icons.Rounded.Check, stringResource(R.string.selected), tint = Vela.Sea, modifier = Modifier.padding(start = 6.dp).size(20.dp))
    }
}

/** Servers run by the embedded Xray engine. */
@Composable
private fun XrayBadge() {
    Text(
        "Xray", color = Vela.Flare, fontSize = 10.sp, fontWeight = FontWeight.SemiBold, maxLines = 1,
        modifier = Modifier.padding(start = 6.dp).clip(RoundedCornerShape(5.dp))
            .border(1.dp, Vela.Flare.copy(alpha = 0.5f), RoundedCornerShape(5.dp))
            .padding(horizontal = 5.dp, vertical = 0.dp),
    )
}
