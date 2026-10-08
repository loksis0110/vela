package app.vela.client.ui

import android.Manifest
import android.content.ClipboardManager
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
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import androidx.lifecycle.lifecycleScope
import app.vela.client.data.Store
import app.vela.client.vpn.ConnState
import app.vela.client.vpn.VpnManager
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    /** Text arriving from a tapped link or the share sheet, shown in the add dialog. */
    private val incoming = MutableStateFlow<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(Vela.Ink.toArgb()),
            navigationBarStyle = SystemBarStyle.dark(Vela.Ink.toArgb()),
        )
        super.onCreate(savedInstanceState)
        handleIntent(intent)
        lifecycleScope.launch { Store.refreshDue() }
        setContent { VelaTheme { App(incoming) } }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    private fun handleIntent(intent: Intent?) {
        val text = when (intent?.action) {
            Intent.ACTION_VIEW -> intent.dataString?.let { decodeVelaLink(it) }
            Intent.ACTION_SEND -> intent.getStringExtra(Intent.EXTRA_TEXT)
            else -> null
        }
        if (!text.isNullOrBlank()) incoming.value = text.trim()
    }

    /** vela://import/<url-encoded subscription or link> */
    private fun decodeVelaLink(link: String): String =
        if (link.startsWith("vela://import/")) Uri.decode(link.removePrefix("vela://import/")) else link
}

private enum class Screen { Home, Servers, Settings }

@Composable
private fun App(incoming: MutableStateFlow<String?>) {
    val context = androidx.compose.ui.platform.LocalContext.current
    val scope = rememberCoroutineScope()
    val app by Store.state.collectAsState()
    val status by VpnManager.status.collectAsState()
    val pings by Store.pings.collectAsState()
    val reasons by Store.reasons.collectAsState()
    val probe by VpnManager.probe.collectAsState()
    val pendingText by incoming.collectAsState()

    var screen by remember { mutableStateOf(Screen.Home) }
    var addOpen by remember { mutableStateOf(false) }
    var addText by remember { mutableStateOf("") }
    var privacyOpen by remember { mutableStateOf(false) }
    var refreshing by remember { mutableStateOf(false) }

    fun toast(msg: String) = Toast.makeText(context, msg, Toast.LENGTH_SHORT).show()

    val notifPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { }
    val vpnPermission = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == android.app.Activity.RESULT_OK) VpnManager.start(context)
        else toast("Без разрешения VPN подключиться нельзя")
    }

    fun connect() {
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        val ask = VpnManager.prepare(context)
        if (ask != null) vpnPermission.launch(ask) else VpnManager.start(context)
    }

    fun reconnectIfNeeded() {
        if (VpnManager.status.value.state == ConnState.Connected) {
            VpnManager.stop(context)
            scope.launch {
                VpnManager.status.first { it.state == ConnState.Disconnected }
                connect()
            }
        }
    }

    LaunchedEffect(pendingText) {
        pendingText?.let {
            addText = it
            addOpen = true
            incoming.value = null
        }
    }

    BackHandler(enabled = screen != Screen.Home) { screen = Screen.Home }

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
            )
            Screen.Servers -> ServersScreen(
                app = app, status = status, pings = pings, reasons = reasons, probe = probe,
                onBack = { screen = Screen.Home },
                onAdd = { addText = ""; addOpen = true },
                onPing = { id ->
                    scope.launch {
                        VpnManager.pingAll(id)
                        val p = Store.state.value.profiles.firstOrNull { it.id == id }
                        if (p != null) toast("Работают ${p.nodes.count { (Store.pings.value[it.id] ?: 0) > 0 }} из ${p.nodes.size}")
                    }
                },
                onRefreshAll = {
                    scope.launch {
                        refreshing = true
                        runCatching { Store.refreshAll() }
                            .onSuccess { toast(it) }
                            .onFailure { toast(it.message ?: "Не удалось обновить") }
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
                            .onSuccess { toast("Подписка обновлена"); if (p.id == app.activeProfile) reconnectIfNeeded() }
                            .onFailure { toast(it.message ?: "Не удалось обновить") }
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
            )
            Screen.Settings -> SettingsScreen(
                settings = app.settings,
                onChange = { Store.setSettings(it); reconnectIfNeeded() },
                onBack = { screen = Screen.Home },
                onPrivacy = { privacyOpen = true },
                onSystemVpnSettings = {
                    runCatching { context.startActivity(Intent(AndroidSettings.ACTION_VPN_SETTINGS)) }
                },
            )
        }
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
                        .onFailure { done(it.message ?: "Ошибка") }
                }
            },
        )
    }

    if (privacyOpen) {
        AlertDialog(
            onDismissRequest = { privacyOpen = false },
            title = { Text("Конфиденциальность") },
            text = {
                Text(
                    "Vela хранит список серверов и настройки только на этом устройстве и исключает их из резервных копий. " +
                        "Приложение не собирает аналитику, не показывает рекламу и не отправляет данные о посещаемых сайтах ни нам, ни третьим лицам.\n\n" +
                        "Трафик идёт на сервер, который вы добавили сами. Его владелец отвечает за то, как обрабатываются данные.",
                )
            },
            confirmButton = { TextButton(onClick = { privacyOpen = false }) { Text("Понятно") } },
            containerColor = Vela.Deep,
        )
    }
}

@Composable
private fun AddDialog(initial: String, onDismiss: () -> Unit, onSubmit: (String, (String?) -> Unit) -> Unit) {
    val context = androidx.compose.ui.platform.LocalContext.current
    var text by remember { mutableStateOf(initial) }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        containerColor = Vela.Deep,
        title = { Text("Добавить серверы") },
        text = {
            Column {
                Text(
                    "Вставьте ссылки на подписки или на серверы: vless, vmess, trojan, ss, hysteria2, tuic. Несколько штук можно вставить сразу, каждую с новой строки.",
                    color = Vela.Muted, fontSize = 13.sp,
                )
                OutlinedTextField(
                    value = text, onValueChange = { text = it; error = null },
                    placeholder = { Text("https://… или vless://…", color = Vela.Muted) },
                    textStyle = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 12.5.sp),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                    isError = error != null,
                    colors = OutlinedTextFieldDefaults.colors(focusedBorderColor = Vela.Star, unfocusedBorderColor = Vela.Line, cursorColor = Vela.Star),
                    modifier = Modifier.fillMaxWidth().heightIn(min = 120.dp).padding(top = 12.dp),
                )
                error?.let { Text(it, color = Vela.Danger, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp)) }
                Row(Modifier.padding(top = 4.dp)) {
                    TextButton(onClick = {
                        val clip = context.getSystemService(ClipboardManager::class.java).primaryClip
                        val pasted = clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(context)?.toString()
                        if (pasted.isNullOrBlank()) error = "Буфер обмена пуст" else text = pasted.trim()
                    }) { Text("Вставить из буфера", color = Vela.Star) }
                    Spacer(Modifier.weight(1f))
                }
            }
        },
        confirmButton = {
            Button(
                enabled = !busy && text.isNotBlank(),
                onClick = {
                    busy = true
                    onSubmit(text) { err -> busy = false; error = err }
                },
            ) { Text(if (busy) "Загружаем…" else "Добавить") }
        },
        dismissButton = { TextButton(onClick = onDismiss, enabled = !busy) { Text("Отмена", color = Vela.Muted) } },
    )
}
