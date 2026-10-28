package app.vela.client.ui

import android.Manifest
import android.content.ClipDescription
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.graphics.drawable.ColorDrawable
import android.app.UiModeManager
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
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewModelScope
import app.vela.client.Lang
import app.vela.client.R
import app.vela.client.data.Store
import app.vela.client.notify.Reminders
import app.vela.client.vpn.ConnState
import app.vela.client.vpn.Health
import app.vela.client.vpn.SpeedState
import app.vela.client.vpn.VpnManager
import app.vela.velacore.Velacore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.security.MessageDigest

/** Text that arrived from outside: a tapped link or the share sheet. */
data class Incoming(val text: String, val isLink: Boolean)

/**
 * What must outlive the activity when Android recreates it (rotation, language,
 * dark mode): a link waiting for confirmation, and work started from the screen
 * (adding, refreshing, checking servers) that would otherwise be cancelled.
 */
class MainModel : ViewModel() {
    /** A tapped app link or shared text, waiting to be looked at. */
    val incoming = MutableStateFlow<Incoming?>(null)

    /** Something importable found in the clipboard. */
    val clipOffer = MutableStateFlow<ImportOffer?>(null)

    /** Set when the widget or tile asks to connect but VPN permission is missing. */
    val connectRequest = MutableStateFlow(false)

    /** An app link or shared text waiting for «Add». */
    val confirm = MutableStateFlow<ImportOffer?>(null)
    val linkError = MutableStateFlow<String?>(null)
    val refreshing = MutableStateFlow(false)
    val clipBusy = MutableStateFlow(false)

    /** The «Add» window; kept here so adding that ends after a rotation still closes it. */
    val addOpen = MutableStateFlow(false)
    val addText = MutableStateFlow("")

    /** Something from the «Add» window or a confirmed link is being added. */
    val adding = MutableStateFlow(false)

    /** Connection state whose side effects (IP check, speed reset) are done. */
    var handledState: ConnState? = null

    /** Server for which the IP was checked after connecting (once traffic really went through). */
    var ipCheckedFor: String? = null

    fun launch(block: suspend CoroutineScope.() -> Unit): Job = viewModelScope.launch(block = block)
}

class MainActivity : ComponentActivity() {
    private val model by lazy { ViewModelProvider(this)[MainModel::class.java] }

    override fun attachBaseContext(newBase: Context) {
        super.attachBaseContext(Lang.wrap(newBase))
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        val theme = Store.state.value.theme
        val systemDark = (resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK) == Configuration.UI_MODE_NIGHT_YES
        applyWindowTheme(theme, if (theme == "system") systemDark else theme == "dark")
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
            val st by Store.state.collectAsStateWithLifecycle()
            VelaTheme(st.theme) { App(model) }
        }
    }

    override fun onStart() {
        super.onStart()
        // per-second statistics of the running VPN are asked only while visible
        VpnManager.onUiStarted()
    }

    override fun onStop() {
        super.onStop()
        VpnManager.onUiStopped()
        // Android may end the process any time now: nothing changed may be lost
        Store.flush()
    }

    /**
     * Colors of the window behind the content and of the system bars. On
     * Android 12+ the system also learns the theme chosen in Vela, so the
     * splash screen of the next start is light or dark accordingly.
     */
    fun applyWindowTheme(theme: String, dark: Boolean) {
        val p = if (dark) NightPalette else DayPalette
        window.setBackgroundDrawable(ColorDrawable(p.ink.toArgb()))
        val bar = if (dark) SystemBarStyle.dark(p.ink.toArgb()) else SystemBarStyle.light(p.ink.toArgb(), p.text.toArgb())
        enableEdgeToEdge(statusBarStyle = bar, navigationBarStyle = bar)
        if (Build.VERSION.SDK_INT >= 31) {
            val mode = when (theme) {
                "light" -> UiModeManager.MODE_NIGHT_NO
                "dark" -> UiModeManager.MODE_NIGHT_YES
                else -> UiModeManager.MODE_NIGHT_AUTO
            }
            if (Store.prefs.getInt(NIGHT_MODE, -1) != mode) {
                runCatching { getSystemService(UiModeManager::class.java)?.setApplicationNightMode(mode) }
                    .onSuccess { Store.prefs.edit().putInt(NIGHT_MODE, mode).apply() }
            }
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
            ACTION_CONNECT -> model.connectRequest.value = true
            Intent.ACTION_VIEW -> intent.dataString?.takeIf { it.isNotBlank() }?.let { model.incoming.value = Incoming(it.trim(), true) }
            Intent.ACTION_SEND -> runCatching { intent.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString() }.getOrNull()
                ?.takeIf { it.isNotBlank() }?.let { model.incoming.value = Incoming(it.trim(), false) }
        }
    }

    /** Offers a subscription or server link copied in a browser or messenger. */
    private fun checkClipboard() {
        if (Store.state.value.noClipboard || model.incoming.value != null || model.clipOffer.value != null || model.confirm.value != null) return
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
            if (model.incoming.value == null && model.confirm.value == null) model.clipOffer.value = offer
        }
    }

    private fun sha256(text: String): String =
        MessageDigest.getInstance("SHA-256").digest(text.toByteArray()).take(12).joinToString("") { "%02x".format(it) }

    companion object {
        const val ACTION_CONNECT = "app.vela.client.CONNECT"
        private const val CLIP_STAMP = "clipStamp"
        private const val CLIP_HASH = "clipHash"
        private const val NIGHT_MODE = "nightMode"
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
private fun App(model: MainModel) {
    val context = androidx.compose.ui.platform.LocalContext.current
    // only while the app is on screen: nothing is redrawn in the background
    val app by Store.state.collectAsStateWithLifecycle()
    val status by VpnManager.status.collectAsStateWithLifecycle()
    val pings by Store.pings.collectAsStateWithLifecycle()
    val reasons by Store.reasons.collectAsStateWithLifecycle()
    val probe by VpnManager.probe.collectAsStateWithLifecycle()
    val clip by model.clipOffer.collectAsStateWithLifecycle()
    val wantConnect by model.connectRequest.collectAsStateWithLifecycle()
    val history by VpnManager.history.collectAsStateWithLifecycle()
    val ip by VpnManager.ip.collectAsStateWithLifecycle()
    val ipBusy by VpnManager.ipBusy.collectAsStateWithLifecycle()
    val ipError by VpnManager.ipError.collectAsStateWithLifecycle()
    val speed by VpnManager.speed.collectAsStateWithLifecycle()
    val dismissed by Store.dismissedAnnounce.collectAsStateWithLifecycle()
    val confirm by model.confirm.collectAsStateWithLifecycle()
    val linkError by model.linkError.collectAsStateWithLifecycle()
    val refreshing by model.refreshing.collectAsStateWithLifecycle()
    val clipBusy by model.clipBusy.collectAsStateWithLifecycle()
    var share by remember { mutableStateOf<Triple<String, String, String>?>(null) }

    val activity = context as? ComponentActivity
    val dark = Vela.isDark
    LaunchedEffect(dark, app.theme) {
        (activity as? MainActivity)?.applyWindowTheme(app.theme, dark)
    }

    var screen by rememberSaveable { mutableStateOf(Screen.Home) }
    val addOpen by model.addOpen.collectAsStateWithLifecycle()
    val addText by model.addText.collectAsStateWithLifecycle()
    val adding by model.adding.collectAsStateWithLifecycle()
    fun openAdd(text: String = "") {
        model.addText.value = text
        model.addOpen.value = true
    }
    var privacyOpen by remember { mutableStateOf(false) }
    var askNotifications by remember { mutableStateOf(false) }
    var language by remember { mutableStateOf(Lang.choice(context)) }
    val lastClip = remember { arrayOfNulls<ImportOffer>(1) }

    fun toast(msg: String) = Toast.makeText(context.applicationContext, msg, Toast.LENGTH_SHORT).show()
    fun str(id: Int, vararg args: Any): String = context.getString(id, *args)

    val notifPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { }
    val reminderPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) Reminders.onSubscriptionsChanged(context)
    }

    fun notificationsAllowed() = Build.VERSION.SDK_INT < 33 ||
        ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

    fun startVpn() {
        VpnManager.start(context)
        // the connection notification (status, «Disconnect») needs this on Android 13+;
        // asked after the VPN dialog, never on top of it
        if (!notificationsAllowed()) runCatching { notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS) }
    }

    val vpnPermission = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == android.app.Activity.RESULT_OK && VpnManager.prepare(context) == null) startVpn()
        else toast(str(R.string.vpn_permission_needed))
    }

    fun connect() {
        val ask = runCatching { VpnManager.prepare(context) }.getOrElse {
            toast(str(R.string.vpn_permission_needed))
            return
        }
        // some devices have no VPN dialog at all
        if (ask == null) startVpn()
        else runCatching { vpnPermission.launch(ask) }.onFailure { toast(str(R.string.vpn_permission_needed)) }
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
            model.connectRequest.value = false
            if (VpnManager.status.value.state == ConnState.Disconnected && app.profiles.isNotEmpty()) connect()
        }
    }

    // what websites see changes with the connection; only on a real change,
    // not when the screen is rotated (that kept wiping the speed test result)
    LaunchedEffect(status.state) {
        val state = status.state
        if (model.handledState == state) return@LaunchedEffect
        when (state) {
            // the address before connecting is no longer what websites see
            ConnState.Connecting, ConnState.Disconnected -> {
                VpnManager.ip.value = null
                VpnManager.ipError.value = null
                model.ipCheckedFor = null
            }
            else -> {}
        }
        if (!VpnManager.speed.value.running) VpnManager.speed.value = SpeedState()
        model.handledState = state
    }
    // asked once traffic really goes through the server, not after a fixed pause
    // (a dead server used to end in an «IP check failed» error instead), and
    // again when another server took over (switch, failover)
    // (automatic selection names its server only after its first round of tests)
    val ipKey = if (status.state == ConnState.Connected && status.health == Health.Ok) status.activeNode.orEmpty() else null
    LaunchedEffect(ipKey) {
        if (ipKey != null && ipKey != model.ipCheckedFor) {
            model.ipCheckedFor = ipKey
            model.launch { VpnManager.refreshIp() }
        }
    }

    // a tapped app link or shared text: confirm what it adds
    LaunchedEffect(Unit) {
        model.incoming.filterNotNull().collect { item ->
            model.clipOffer.value = null
            val result = withContext(Dispatchers.Default) {
                runCatching {
                    if (item.isLink) ImportOffer.from(Velacore.parseDeepLink(item.text))
                    else ImportOffer.from(Velacore.detectImport(item.text))
                }
            }
            // taken only now: if the activity is recreated meanwhile, the new one looks at it again
            model.incoming.compareAndSet(item, null)
            val offer = result.getOrNull()
            if (offer != null && !offer.isEmpty) {
                model.linkError.value = null
                model.confirm.value = offer
            } else if (!item.isLink) {
                // shared text the core doesn't recognize (or a backup): let the user look at it
                openAdd(item.text)
            } else {
                model.linkError.value = result.exceptionOrNull()?.message ?: str(R.string.link_nothing)
            }
        }
    }

    // added in the meantime (e.g. through the link itself): nothing to offer any more
    LaunchedEffect(app.profiles) {
        val offer = model.clipOffer.value ?: return@LaunchedEffect
        val rest = withContext(Dispatchers.Default) { offer.withoutKnown() }
        if (rest.isEmpty && model.clipOffer.value == offer) model.clipOffer.value = null
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
                            ConnState.Disconnected -> if (app.profiles.isEmpty()) openAdd() else connect()
                            ConnState.Connected, ConnState.Connecting -> VpnManager.stop(context)
                            ConnState.Disconnecting -> {}
                        }
                    },
                    onServers = { if (app.profiles.isEmpty()) openAdd() else screen = Screen.Servers },
                    onSettings = { screen = Screen.Settings },
                    onRouting = { r ->
                        if (r != app.settings.routing) {
                            Store.setSettings(app.settings.copy(routing = r))
                            reconnectIfNeeded()
                        }
                    },
                    onDismissError = { VpnManager.status.update { it.copy(error = null) } },
                    onDismissNotice = { VpnManager.status.update { it.copy(notice = null) } },
                    history = history, ip = ip, ipBusy = ipBusy, ipError = ipError, speed = speed,
                    onIp = { model.launch { VpnManager.refreshIp() } },
                    onSpeed = { model.launch { VpnManager.runSpeedTest() } },
                    announce = Store.visibleAnnounce(app.active, dismissed),
                    onDismissAnnounce = { app.active?.let { Store.dismissAnnounce(it.id, it.announce) } },
                    onStats = { screen = Screen.Stats },
                    onConnections = { screen = Screen.Connections },
                )
                Screen.Servers -> ServersScreen(
                    app = app, status = status, pings = pings, reasons = reasons, probe = probe,
                    onBack = { screen = Screen.Home },
                    onAdd = { openAdd() },
                    onPing = { id ->
                        model.launch {
                            VpnManager.pingAll(id)
                            val p = Store.state.value.profiles.firstOrNull { it.id == id }
                            if (p != null) toast(str(R.string.probe_result, p.nodes.count { (Store.pings.value[it.id] ?: 0) > 0 }, p.nodes.size))
                        }
                    },
                    onRefreshAll = {
                        if (!model.refreshing.value) model.launch {
                            model.refreshing.value = true
                            try {
                                runCatching { Store.refreshAll() }
                                    .onSuccess { toast(it) }
                                    .onFailure { toast(it.message ?: str(R.string.refresh_failed)) }
                            } finally {
                                model.refreshing.value = false
                            }
                        }
                    },
                    onRename = { p, name -> Store.rename(p.id, name) },
                    onSelect = { profileId, nodeId ->
                        val sameProfile = profileId == app.activeProfile
                        Store.select(profileId, nodeId)
                        if (status.state == ConnState.Connected) {
                            model.launch {
                                if (!sameProfile || !VpnManager.switchTo(nodeId)) reconnectIfNeeded()
                            }
                        }
                        screen = Screen.Home
                    },
                    onRefresh = { p ->
                        if (!model.refreshing.value) model.launch {
                            model.refreshing.value = true
                            try {
                                runCatching { Store.refresh(p.id) }
                                    // the running VPN is reloaded by the store, only if the servers changed
                                    .onSuccess { toast(str(R.string.refresh_done)) }
                                    .onFailure { toast(it.message ?: str(R.string.refresh_failed)) }
                            } finally {
                                model.refreshing.value = false
                            }
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
                    onPingOne = { pid, nid -> model.launch { VpnManager.pingOne(pid, nid) } },
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
                    onImport = { openAdd() },
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
                        model.clipBusy.value = true
                        model.launch {
                            try {
                                runCatching { addOffer(offer, null) }
                                    .onSuccess { toast(it) }
                                    .onFailure { toast(it.message ?: str(R.string.error_generic)) }
                            } finally {
                                model.clipBusy.value = false
                                model.clipOffer.value = null
                            }
                        }
                    },
                    onDismiss = { model.clipOffer.value = null },
                )
            }
        }
    }

    confirm?.let { offer ->
        ImportConfirmDialog(
            offer = offer,
            onDismiss = { model.confirm.value = null },
            onConfirm = { preview, done ->
                model.adding.value = true
                model.launch {
                    try {
                        runCatching { addOffer(offer, preview) }
                            .onSuccess { msg ->
                                model.confirm.compareAndSet(offer, null)
                                toast(msg)
                                done(null)
                            }
                            .onFailure { done(it.message ?: str(R.string.error_generic)) }
                    } finally {
                        model.adding.value = false
                    }
                }
            },
            working = adding,
        )
    }

    linkError?.let { err ->
        AlertDialog(
            onDismissRequest = { model.linkError.value = null },
            containerColor = Vela.Deep,
            title = { Text(stringResource(R.string.link_error_title)) },
            text = { Text(err) },
            confirmButton = { TextButton(onClick = { model.linkError.value = null }) { Text(stringResource(R.string.ok)) } },
        )
    }

    if (addOpen) {
        AddDialog(
            initial = addText,
            onDismiss = { model.addOpen.value = false },
            onSubmit = { text, done ->
                model.adding.value = true
                model.launch {
                    try {
                        runCatching { Store.import(text) }
                            .onSuccess { msg ->
                                model.addOpen.value = false
                                toast(msg)
                                done(null)
                            }
                            .onFailure { done(it.message ?: str(R.string.error_generic)) }
                    } finally {
                        model.adding.value = false
                    }
                }
            },
            working = adding,
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
