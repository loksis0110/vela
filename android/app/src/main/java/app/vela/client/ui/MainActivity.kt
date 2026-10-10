package app.vela.client.ui

import android.Manifest
import android.content.ClipDescription
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings as AndroidSettings
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import app.vela.client.Lang
import app.vela.client.R
import app.vela.client.data.Store
import app.vela.client.notify.Reminders
import app.vela.client.vpn.ConnState
import app.vela.client.vpn.SpeedState
import app.vela.client.vpn.VpnManager
import app.vela.velacore.Velacore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.security.MessageDigest

/** Text that arrived from outside: a tapped link or the share sheet. */
data class Incoming(val text: String, val isLink: Boolean)

class MainActivity : ComponentActivity() {
    /** A tapped app link or shared text, waiting to be confirmed. */
    private val incoming = MutableStateFlow<Incoming?>(null)

    /** Something importable found in the clipboard. */
    private val clipOffer = MutableStateFlow<ImportOffer?>(null)

    /** Set when the widget or tile asks to connect but VPN permission is missing. */
    private val connectRequest = MutableStateFlow(false)

    override fun attachBaseContext(newBase: Context) {
        super.attachBaseContext(Lang.wrap(newBase))
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(Vela.Ink.toArgb()),
            navigationBarStyle = SystemBarStyle.dark(Vela.Ink.toArgb()),
        )
        super.onCreate(savedInstanceState)
        Lang.applyToCore()
        // only a fresh launch: rotation or a theme change must not repeat a shared
        // link or a widget's «connect»
        val fromHistory = intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY != 0
        if (savedInstanceState == null && !fromHistory) handleIntent(intent)
        // subscriptions are refreshed while the app is open, as often as set
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                while (true) {
                    Store.refreshDue()
                    delay(10 * 60 * 1000L)
                }
            }
        }
        setContent {
            val st by Store.state.collectAsState()
            VelaTheme(st.theme) { App(incoming, clipOffer, connectRequest) }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        // Android 10+ lets apps read the clipboard only while their window has focus
        if (hasFocus) checkClipboard()
    }

    private fun handleIntent(intent: Intent?) {
        when (intent?.action) {
            ACTION_CONNECT -> connectRequest.value = true
            Intent.ACTION_VIEW -> intent.dataString?.takeIf { it.isNotBlank() }?.let { incoming.value = Incoming(it.trim(), true) }
            Intent.ACTION_SEND -> intent.getStringExtra(Intent.EXTRA_TEXT)?.takeIf { it.isNotBlank() }?.let {
                incoming.value = Incoming(it.trim(), false)
            }
        }
    }

    /** Offers a subscription or server link copied in a browser or messenger. */
    private fun checkClipboard() {
        if (Store.state.value.noClipboard || incoming.value != null || clipOffer.value != null) return
        val cm = getSystemService(ClipboardManager::class.java) ?: return
        val desc = runCatching { cm.primaryClipDescription }.getOrNull() ?: return
        val textual = desc.hasMimeType(ClipDescription.MIMETYPE_TEXT_PLAIN) ||
            desc.hasMimeType(ClipDescription.MIMETYPE_TEXT_HTML) || desc.hasMimeType(ClipDescription.MIMETYPE_TEXT_URILIST)
        if (!textual) return
        // each clip is read once: Android 12+ shows a notice on every read
        val stamp = if (Build.VERSION.SDK_INT >= 26) desc.timestamp else 0L
        if (stamp != 0L && stamp == Store.prefs.getLong(CLIP_STAMP, -1L)) return
        Store.prefs.edit().putLong(CLIP_STAMP, stamp).apply()
        val text = runCatching {
            cm.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(this)?.toString()
        }.getOrNull()?.trim()
        if (text.isNullOrEmpty() || text.length > 500_000) return
        val hash = sha256(text)
        if (hash == Store.prefs.getString(CLIP_HASH, "")) return
        lifecycleScope.launch {
            val offer = withContext(Dispatchers.Default) {
                runCatching { ImportOffer.from(Velacore.detectImport(text)).withoutKnown() }.getOrNull()
            } ?: return@launch
            if (offer.isEmpty) return@launch
            // offered once; asking again about the same text would be annoying
            Store.prefs.edit().putString(CLIP_HASH, hash).apply()
            if (incoming.value == null) clipOffer.value = offer
        }
    }

    private fun sha256(text: String): String =
        MessageDigest.getInstance("SHA-256").digest(text.toByteArray()).take(12).joinToString("") { "%02x".format(it) }

    companion object {
        const val ACTION_CONNECT = "app.vela.client.CONNECT"
        private const val CLIP_STAMP = "clipStamp"
        private const val CLIP_HASH = "clipHash"
    }
}

private enum class Screen { Home, Servers, Settings, Stats, Connections }

private object LaunchState {
    var handled = false
}

/** Adds what a confirmed offer holds; a downloaded subscription is not fetched again. */
private suspend fun addOffer(offer: ImportOffer, preview: JSONObject?): String {
    val single = offer.subscriptions.singleOrNull()
    if (single != null && preview != null) {
        val parts = mutableListOf(Store.addFetched(single, preview, offer.name))
        if (offer.links.isNotBlank()) parts += Store.import(offer.links)
        return parts.joinToString(". ")
    }
    val msg = Store.import(offer.text)
    if (single != null) {
        Store.state.value.profiles.firstOrNull { it.kind == "subscription" && it.url == single }?.let {
            Store.applySuggestedName(it.id, offer.name)
        }
    }
    return msg
}

@Composable
private fun App(
    incoming: MutableStateFlow<Incoming?>,
    clipOffer: MutableStateFlow<ImportOffer?>,
    connectRequest: MutableStateFlow<Boolean>,
) {
    val context = androidx.compose.ui.platform.LocalContext.current
    val scope = rememberCoroutineScope()
    val app by Store.state.collectAsState()
    val status by VpnManager.status.collectAsState()
    val pings by Store.pings.collectAsState()
    val reasons by Store.reasons.collectAsState()
    val probe by VpnManager.probe.collectAsState()
    val clip by clipOffer.collectAsState()
    val wantConnect by connectRequest.collectAsState()
    val history by VpnManager.history.collectAsState()
    val ip by VpnManager.ip.collectAsState()
    val ipBusy by VpnManager.ipBusy.collectAsState()
    val ipError by VpnManager.ipError.collectAsState()
    val speed by VpnManager.speed.collectAsState()
    val dismissed by Store.dismissedAnnounce.collectAsState()
    var share by remember { mutableStateOf<Triple<String, String, String>?>(null) }

    val activity = context as? ComponentActivity
    val dark = Vela.isDark
    LaunchedEffect(dark) {
        val bar = if (dark) SystemBarStyle.dark(Vela.Ink.toArgb()) else SystemBarStyle.light(Vela.Ink.toArgb(), Vela.Text.toArgb())
        activity?.enableEdgeToEdge(statusBarStyle = bar, navigationBarStyle = bar)
    }

    var screen by rememberSaveable { mutableStateOf(Screen.Home) }
    var addOpen by rememberSaveable { mutableStateOf(false) }
    var addText by rememberSaveable { mutableStateOf("") }
    var privacyOpen by remember { mutableStateOf(false) }
    var refreshing by remember { mutableStateOf(false) }
    var confirm by remember { mutableStateOf<ImportOffer?>(null) }
    var linkError by remember { mutableStateOf<String?>(null) }
    var clipBusy by remember { mutableStateOf(false) }
    var askNotifications by remember { mutableStateOf(false) }
    var language by remember { mutableStateOf(Lang.choice(context)) }
    val lastClip = remember { arrayOfNulls<ImportOffer>(1) }

    fun toast(msg: String) = Toast.makeText(context, msg, Toast.LENGTH_SHORT).show()
    fun str(id: Int, vararg args: Any): String = context.getString(id, *args)

    val notifPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { }
    val reminderPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) Reminders.onSubscriptionsChanged(context)
    }
    val vpnPermission = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == android.app.Activity.RESULT_OK) VpnManager.start(context)
        else toast(str(R.string.vpn_permission_needed))
    }

    fun notificationsAllowed() = Build.VERSION.SDK_INT < 33 ||
        ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

    fun connect() {
        if (!notificationsAllowed()) notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        val ask = VpnManager.prepare(context)
        if (ask != null) vpnPermission.launch(ask) else VpnManager.start(context)
    }

    fun reconnectIfNeeded() {
        // the running service rebuilds its config in place
        if (VpnManager.status.value.state == ConnState.Connected) VpnManager.restart(context)
    }

    // «Подключаться при запуске»: once per app start, not on every rotation
    LaunchedEffect(Unit) {
        if (!LaunchState.handled) {
            LaunchState.handled = true
            if (app.connectOnLaunch && app.profiles.isNotEmpty() && VpnManager.status.value.state == ConnState.Disconnected) connect()
        }
    }

    LaunchedEffect(wantConnect) {
        if (wantConnect) {
            connectRequest.value = false
            if (VpnManager.status.value.state == ConnState.Disconnected && app.profiles.isNotEmpty()) connect()
        }
    }

    // what websites see changes with the connection
    LaunchedEffect(status.state) {
        when (status.state) {
            ConnState.Connected -> {
                delay(1200)
                VpnManager.refreshIp()
            }
            ConnState.Disconnected -> {
                VpnManager.ip.value = null
                VpnManager.ipError.value = null
            }
            else -> {}
        }
        if (!VpnManager.speed.value.running) VpnManager.speed.value = SpeedState()
    }

    // a tapped app link or shared text: confirm what it adds
    LaunchedEffect(Unit) {
        incoming.collect { item ->
            if (item == null) return@collect
            incoming.value = null
            clipOffer.value = null
            val result = withContext(Dispatchers.Default) {
                runCatching {
                    if (item.isLink) ImportOffer.from(Velacore.parseDeepLink(item.text))
                    else ImportOffer.from(Velacore.detectImport(item.text))
                }
            }
            val offer = result.getOrNull()
            if (offer != null && !offer.isEmpty) {
                confirm = offer
            } else if (!item.isLink) {
                // shared text the core doesn't recognize (or a backup): let the user look at it
                addText = item.text
                addOpen = true
            } else {
                linkError = result.exceptionOrNull()?.message ?: str(R.string.link_nothing)
            }
        }
    }

    // added in the meantime (e.g. through the link itself): nothing to offer any more
    LaunchedEffect(app.profiles) {
        val offer = clipOffer.value ?: return@LaunchedEffect
        val rest = withContext(Dispatchers.Default) { offer.withoutKnown() }
        if (rest.isEmpty && clipOffer.value == offer) clipOffer.value = null
    }

    // reminders need the notification permission (Android 13+): ask once, politely
    val hasExpiry = app.profiles.any { it.kind == "subscription" && it.expire > 0 }
    LaunchedEffect(hasExpiry, app.noReminders) {
        if (hasExpiry && !app.noReminders && !notificationsAllowed() && !Store.prefs.getBoolean("notifAsked", false)) {
            delay(1500)
            askNotifications = true
        }
    }

    BackHandler(enabled = screen != Screen.Home) { screen = Screen.Home }

    Box(Modifier.fillMaxSize()) {
        AnimatedContent(
            targetState = screen,
            transitionSpec = {
                if (targetState != Screen.Home) slideInHorizontally { it / 8 } + fadeIn() togetherWith fadeOut()
                else fadeIn() togetherWith fadeOut()
            },
            label = "screen",
        ) { s ->
            when (s) {
                Screen.Home -> HomeScreen(
                    app = app, status = status, pings = pings,
                    onToggle = {
                        when (status.state) {
                            ConnState.Disconnected -> if (app.profiles.isEmpty()) addOpen = true else connect()
                            ConnState.Connected, ConnState.Connecting -> VpnManager.stop(context)
                            ConnState.Disconnecting -> {}
                        }
                    },
                    onServers = { if (app.profiles.isEmpty()) addOpen = true else screen = Screen.Servers },
                    onSettings = { screen = Screen.Settings },
                    onRouting = { r ->
                        if (r != app.settings.routing) {
                            Store.setSettings(app.settings.copy(routing = r))
                            reconnectIfNeeded()
                        }
                    },
                    onDismissError = { VpnManager.status.value = VpnManager.status.value.copy(error = null) },
                    onDismissNotice = { VpnManager.status.value = VpnManager.status.value.copy(notice = null) },
                    history = history, ip = ip, ipBusy = ipBusy, ipError = ipError, speed = speed,
                    onIp = { scope.launch { VpnManager.refreshIp() } },
                    onSpeed = { scope.launch { VpnManager.runSpeedTest() } },
                    announce = Store.visibleAnnounce(app.active, dismissed),
                    onDismissAnnounce = { app.active?.let { Store.dismissAnnounce(it.id, it.announce) } },
                    onStats = { screen = Screen.Stats },
                    onConnections = { screen = Screen.Connections },
                )
                Screen.Servers -> ServersScreen(
                    app = app, status = status, pings = pings, reasons = reasons, probe = probe,
                    onBack = { screen = Screen.Home },
                    onAdd = { addText = ""; addOpen = true },
                    onPing = { id ->
                        scope.launch {
                            VpnManager.pingAll(id)
                            val p = Store.state.value.profiles.firstOrNull { it.id == id }
                            if (p != null) toast(str(R.string.probe_result, p.nodes.count { (Store.pings.value[it.id] ?: 0) > 0 }, p.nodes.size))
                        }
                    },
                    onRefreshAll = {
                        scope.launch {
                            refreshing = true
                            runCatching { Store.refreshAll() }
                                .onSuccess { toast(it) }
                                .onFailure { toast(it.message ?: str(R.string.refresh_failed)) }
                            refreshing = false
                        }
                    },
                    onRename = { p, name -> Store.rename(p.id, name) },
                    onSelect = { profileId, nodeId ->
                        val sameProfile = profileId == app.activeProfile
                        Store.select(profileId, nodeId)
                        if (status.state == ConnState.Connected) {
                            scope.launch {
                                if (!sameProfile || !VpnManager.switchTo(nodeId)) reconnectIfNeeded()
                            }
                        }
                        screen = Screen.Home
                    },
                    onRefresh = { p ->
                        scope.launch {
                            refreshing = true
                            runCatching { Store.refresh(p.id) }
                                .onSuccess { toast(str(R.string.refresh_done)); if (p.id == app.activeProfile) reconnectIfNeeded() }
                                .onFailure { toast(it.message ?: str(R.string.refresh_failed)) }
                            refreshing = false
                        }
                    },
                    onDeleteProfile = { p ->
                        if (p.id == app.activeProfile && status.state != ConnState.Disconnected) VpnManager.stop(context)
                        Store.deleteProfile(p.id)
                    },
                    onDeleteNode = { p, n -> Store.deleteNode(p.id, n.id) },
                    onOpenUrl = { url ->
                        if (url.startsWith("https://") || url.startsWith("http://") || url.startsWith("tg://")) {
                            runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) }
                        }
                    },
                    refreshing = refreshing,
                    onFavorite = { id, on -> Store.setFavorite(id, on) },
                    onShare = { title, text -> share = Triple(title, str(R.string.share_hint), text) },
                    onStopProbe = { VpnManager.stopProbe() },
                    onPingOne = { pid, nid -> scope.launch { VpnManager.pingOne(pid, nid) } },
                    onSort = { Store.setSortByPing(it) },
                )
                Screen.Settings -> SettingsScreen(
                    settings = app.settings,
                    onChange = { Store.setSettings(it); reconnectIfNeeded() },
                    onBack = { screen = Screen.Home },
                    onPrivacy = { privacyOpen = true },
                    onSystemVpnSettings = {
                        runCatching { context.startActivity(Intent(AndroidSettings.ACTION_VPN_SETTINGS)) }
                    },
                    theme = app.theme,
                    onTheme = { Store.setTheme(it) },
                    onExport = {
                        runCatching { Store.exportBackup() }
                            .onSuccess { share = Triple(str(R.string.backup_title), str(R.string.backup_hint), it) }
                            .onFailure { toast(it.message ?: str(R.string.error_generic)) }
                    },
                    onImport = { addText = ""; addOpen = true },
                    sub = app.sub,
                    onSub = { Store.setSubPrefs(it) },
                    connectOnLaunch = app.connectOnLaunch,
                    onConnectOnLaunch = { Store.setConnectOnLaunch(it) },
                    onReset = {
                        Store.resetSettings()
                        reconnectIfNeeded()
                        toast(str(R.string.reset_done))
                    },
                    language = language,
                    onLanguage = { lang ->
                        language = lang
                        activity?.let { Lang.set(it, lang) }
                    },
                    clipboard = !app.noClipboard,
                    onClipboard = { Store.setClipboard(it) },
                    reminders = !app.noReminders,
                    onReminders = { on ->
                        Store.setReminders(on)
                        if (on && !notificationsAllowed()) reminderPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
                    },
                )
                Screen.Stats -> StatsScreen(connected = status.state == ConnState.Connected, onBack = { screen = Screen.Home })
                Screen.Connections -> ConnectionsScreen(app = app, status = status, onBack = { screen = Screen.Home })
            }
        }

        AnimatedVisibility(
            visible = clip != null && confirm == null && !addOpen,
            enter = slideInVertically { it } + fadeIn(),
            exit = slideOutVertically { it } + fadeOut(),
            modifier = Modifier.align(Alignment.BottomCenter).navigationBarsPadding().padding(horizontal = 12.dp, vertical = 12.dp),
        ) {
            // the card keeps showing the last offer while it slides away
            clip?.let { lastClip[0] = it }
            (clip ?: lastClip[0])?.let { offer ->
                ClipboardPrompt(
                    offer = offer, busy = clipBusy,
                    onAdd = {
                        clipBusy = true
                        scope.launch {
                            runCatching { addOffer(offer, null) }
                                .onSuccess { toast(it) }
                                .onFailure { toast(it.message ?: str(R.string.error_generic)) }
                            clipBusy = false
                            clipOffer.value = null
                        }
                    },
                    onDismiss = { clipOffer.value = null },
                )
            }
        }
    }

    confirm?.let { offer ->
        ImportConfirmDialog(
            offer = offer,
            onDismiss = { confirm = null },
            onConfirm = { preview, done ->
                scope.launch {
                    runCatching { addOffer(offer, preview) }
                        .onSuccess { msg ->
                            confirm = null
                            toast(msg)
                            done(null)
                        }
                        .onFailure { done(it.message ?: str(R.string.error_generic)) }
                }
            },
        )
    }

    linkError?.let { err ->
        AlertDialog(
            onDismissRequest = { linkError = null },
            containerColor = Vela.Deep,
            title = { Text(stringResource(R.string.link_error_title)) },
            text = { Text(err) },
            confirmButton = { TextButton(onClick = { linkError = null }) { Text(stringResource(R.string.ok)) } },
        )
    }

    if (addOpen) {
        AddDialog(
            initial = addText,
            onDismiss = { addOpen = false },
            onSubmit = { text, done ->
                scope.launch {
                    runCatching { Store.import(text) }
                        .onSuccess { msg ->
                            addOpen = false
                            toast(msg)
                            done(null)
                        }
                        .onFailure { done(it.message ?: str(R.string.error_generic)) }
                }
            },
        )
    }

    if (askNotifications) {
        AlertDialog(
            onDismissRequest = { askNotifications = false; Store.prefs.edit().putBoolean("notifAsked", true).apply() },
            containerColor = Vela.Deep,
            title = { Text(stringResource(R.string.notif_ask_title)) },
            text = { Text(stringResource(R.string.notif_ask_text)) },
            confirmButton = {
                TextButton(onClick = {
                    askNotifications = false
                    Store.prefs.edit().putBoolean("notifAsked", true).apply()
                    reminderPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
                }) { Text(stringResource(R.string.allow)) }
            },
            dismissButton = {
                TextButton(onClick = {
                    askNotifications = false
                    Store.prefs.edit().putBoolean("notifAsked", true).apply()
                }) { Text(stringResource(R.string.not_now), color = Vela.Muted) }
            },
        )
    }

    share?.let { (title, hint, text) ->
        ShareDialog(title, hint, text) { share = null }
    }

    if (privacyOpen) {
        AlertDialog(
            onDismissRequest = { privacyOpen = false },
            title = { Text(stringResource(R.string.privacy_title)) },
            text = { Text(stringResource(R.string.privacy_text)) },
            confirmButton = { TextButton(onClick = { privacyOpen = false }) { Text(stringResource(R.string.ok_got_it)) } },
            containerColor = Vela.Deep,
        )
    }
}
