package app.vela.client.vpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.ServiceInfo
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.os.PowerManager
import android.system.OsConstants
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import app.vela.client.Lang
import app.vela.client.R
import app.vela.client.data.Store
import app.vela.client.data.VelaError
import app.vela.client.ui.MainActivity
import app.vela.libbox.BridgeOptions
import app.vela.libbox.BridgeSession
import app.vela.libbox.CommandServer
import app.vela.libbox.CommandServerHandler
import app.vela.libbox.ConnectionOwner
import app.vela.libbox.InterfaceUpdateListener
import app.vela.libbox.Libbox
import app.vela.libbox.LocalDNSTransport
import app.vela.libbox.NeighborUpdateListener
import app.vela.libbox.NetworkInterfaceIterator
import app.vela.libbox.OverrideOptions
import app.vela.libbox.PlatformInterface
import app.vela.libbox.PlatformUser
import app.vela.libbox.ShellSession
import app.vela.libbox.StringIterator
import app.vela.libbox.SystemProxyStatus
import app.vela.libbox.TunOptions
import app.vela.libbox.WIFIState
import app.vela.velacore.Velacore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.net.Inet6Address
import java.net.InterfaceAddress
import java.net.NetworkInterface
import app.vela.libbox.Notification as BoxNotification
import app.vela.libbox.NetworkInterface as BoxInterface

class VelaVpnService : VpnService(), PlatformInterface, CommandServerHandler {
    companion object {
        const val ACTION_START = "app.vela.client.START"
        const val ACTION_STOP = "app.vela.client.STOP"
        /** Applies new settings or another server list without stopping the service. */
        const val ACTION_RESTART = "app.vela.client.RESTART"
        private const val CHANNEL = "vpn"
        private const val NOTIFICATION_ID = 1
        private const val TAG = "VelaVpn"

        @Volatile private var running: VelaVpnService? = null

        /** The app language changed: the notification follows it (also while connected). */
        fun onLanguageChanged() {
            val s = running ?: return
            s.post {
                when (VpnManager.status.value.state) {
                    ConnState.Connected -> {
                        s.shownText = ""
                        s.showNotification(s.notificationText(VpnManager.status.value.health), update = true)
                    }
                    ConnState.Connecting -> s.showNotification(Lang.str(R.string.notif_connecting), update = true)
                    else -> {}
                }
            }
        }
    }

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val lock = Mutex()
    private var commandServer: CommandServer? = null
    private var tunFd: ParcelFileDescriptor? = null
    private var statsJob: Job? = null
    private var watchJob: Job? = null
    private var receiverOn = false

    /** Text of the notification now shown, so it is redrawn only on a change. */
    @Volatile private var shownText = ""

    /**
     * Screen on: the connection is checked at once (it was left alone while the
     * screen was off). Doze: the core pauses its timers (automatic selection
     * re-tests), as sing-box's own app does.
     */
    private val receiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) {
            when (intent.action) {
                Intent.ACTION_SCREEN_ON -> VpnManager.recheckSoon(1000)
                PowerManager.ACTION_DEVICE_IDLE_MODE_CHANGED -> {
                    val idle = getSystemService(PowerManager::class.java)?.isDeviceIdleMode == true
                    runCatching { if (idle) commandServer?.pause() else commandServer?.wake() }
                    if (!idle) VpnManager.recheckSoon(1000)
                }
            }
        }
    }

    /** Id of the newest start command: stopping must not cancel a newer «connect». */
    @Volatile private var lastStartId = 0

    /**
     * Commands run one by one in the order they came: «disconnect» quickly
     * followed by «connect» must never be carried out the other way round.
     */
    private val commands = Channel<suspend () -> Unit>(Channel.UNLIMITED)

    private fun post(block: suspend () -> Unit) {
        commands.trySend(block)
    }

    // ------------------------------------------------------------- lifecycle

    override fun attachBaseContext(base: Context) {
        super.attachBaseContext(Lang.wrap(base))
    }

    override fun onCreate() {
        super.onCreate()
        running = this
        scope.launch {
            for (command in commands) {
                runCatching { command() }.onFailure { Log.e(TAG, "command", it) }
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        lastStartId = startId
        when (intent?.action) {
            ACTION_STOP -> post { stopVpn(startId) }
            ACTION_RESTART -> post { restartVpn(startId) }
            // ACTION_START, or null/"android.net.VpnService" when started by Always-on VPN
            else -> {
                // started with startForegroundService: Android requires the
                // notification right away, before the core is up
                if (!showNotification(Lang.str(R.string.notif_connecting))) {
                    if (VpnManager.status.value.state == ConnState.Disconnected) {
                        VpnManager.status.value = VpnStatus(error = Lang.str(R.string.error_vpn_permission))
                        stopSelf(startId)
                    }
                    return START_NOT_STICKY
                }
                post { startVpn(startId) }
            }
        }
        return START_NOT_STICKY
    }

    override fun onRevoke() {
        // another VPN app took over, or the user revoked permission
        val id = lastStartId
        post { stopVpn(id) }
    }

    override fun onDestroy() {
        if (running === this) running = null
        // stopped without «disconnect» (e.g. by the system): don't leave the
        // interface open or the app showing «connected»
        if (VpnManager.status.value.state != ConnState.Disconnected || tunFd != null) {
            cleanup()
            VpnManager.status.value = VpnStatus(state = ConnState.Disconnected)
        }
        runCatching { commandServer?.close() }
        commandServer = null
        commands.close()
        scope.cancel()
        // the process may be killed soon after: «was connected» must be on disk
        Store.flush()
        super.onDestroy()
    }

    private suspend fun startVpn(startId: Int) = lock.withLock {
        val current = VpnManager.status.value.state
        if (current == ConnState.Connected) {
            // e.g. Always-on asked again: keep the right notification text
            showNotification(notificationText(VpnManager.status.value.health))
            return@withLock
        }
        VpnManager.status.value = VpnStatus(state = ConnState.Connecting)
        try {
            // nothing slow before the core: no network requests, no waiting
            val port = freePort()
            val secret = Store.randomId(16)
            val config = Store.buildConfig(port, secret)
            runCatching { Store.logFile.writeText("") }
            VpnManager.history.value = emptyList()
            VpnManager.controllerPort = port
            VpnManager.controllerSecret = secret
            DefaultNetworkMonitor.start()
            val server = commandServer ?: CommandServer(this, this).also {
                it.start()
                commandServer = it
            }
            server.startOrReloadService(config.json, OverrideOptions())
            VpnManager.runningNodes = config.nodeIds
            startRecorder(port, secret)
            Store.setWasConnected(true)
            // the tunnel is up; «protected» only once a request really went through
            VpnManager.status.value = VpnStatus(state = ConnState.Connected, since = System.currentTimeMillis() / 1000, health = Health.Checking)
            showNotification(notificationText(Health.Checking))
            registerReceiverOnce()
            startStats()
            startWatchdog()
        } catch (e: Exception) {
            Log.e(TAG, "start failed", e)
            cleanup()
            VpnManager.status.value = VpnStatus(state = ConnState.Disconnected, error = humanize(e))
            finish(startId)
        }
    }

    private suspend fun restartVpn(startId: Int) = lock.withLock {
        val server = commandServer
        if (server == null || VpnManager.status.value.state != ConnState.Connected) {
            // nothing is running (the process was restarted, or the start failed)
            if (VpnManager.status.value.state == ConnState.Disconnected) stopSelf(startId)
            return@withLock
        }
        try {
            watchJob?.cancel()
            val port = VpnManager.controllerPort.takeIf { it != 0 } ?: freePort()
            val secret = VpnManager.controllerSecret.ifEmpty { Store.randomId(16) }
            VpnManager.controllerPort = port
            VpnManager.controllerSecret = secret
            val config = Store.buildConfig(port, secret)
            // the reloaded core starts counting from zero: save what the old one carried
            runCatching { Velacore.stopTrafficRecorder() }
            server.startOrReloadService(config.json, OverrideOptions())
            VpnManager.runningNodes = config.nodeIds
            startRecorder(port, secret)
            VpnManager.status.update { it.copy(error = null, notice = null, activeNode = null, health = Health.Checking) }
            showNotification(notificationText(Health.Checking), update = true)
            startWatchdog()
        } catch (e: Exception) {
            Log.e(TAG, "restart failed", e)
            cleanup()
            Store.setWasConnected(false)
            VpnManager.status.value = VpnStatus(state = ConnState.Disconnected, error = humanize(e))
            finish(startId)
        }
    }

    private suspend fun stopVpn(startId: Int) = lock.withLock {
        if (VpnManager.status.value.state != ConnState.Disconnected || commandServer != null) {
            VpnManager.status.value = VpnManager.status.value.copy(state = ConnState.Disconnecting)
            Store.setWasConnected(false)
            cleanup()
            VpnManager.status.value = VpnStatus(state = ConnState.Disconnected)
        }
        finish(startId)
    }

    /**
     * Leaves the foreground and stops the service, unless a newer command came
     * in meanwhile (e.g. «connect» right after «disconnect»): that one goes on.
     */
    private fun finish(startId: Int) {
        if (startId != lastStartId) return
        stopForegroundCompat()
        stopSelf(startId)
    }

    private fun cleanup() {
        statsJob?.cancel()
        statsJob = null
        watchJob?.cancel()
        watchJob = null
        // the last numbers are read from the core, so before it stops
        runCatching { Velacore.stopTrafficRecorder() }
        runCatching { commandServer?.closeService() }
        runCatching { tunFd?.close() }
        tunFd = null
        DefaultNetworkMonitor.stop()
        VpnManager.controllerPort = 0
        VpnManager.runningNodes = emptySet()
        if (receiverOn) {
            runCatching { unregisterReceiver(receiver) }
            receiverOn = false
        }
    }

    private fun registerReceiverOnce() {
        if (receiverOn) return
        val filter = IntentFilter().apply {
            addAction(Intent.ACTION_SCREEN_ON)
            addAction(PowerManager.ACTION_DEVICE_IDLE_MODE_CHANGED)
        }
        receiverOn = runCatching {
            ContextCompat.registerReceiver(this, receiver, filter, ContextCompat.RECEIVER_NOT_EXPORTED)
        }.isSuccess
    }

    /** Traffic statistics (Statistics screen) of the running core. */
    private fun startRecorder(port: Int, secret: String) {
        runCatching { Velacore.startTrafficRecorder(port, secret, Store.trafficFile.absolutePath) }
            .onFailure { Log.w(TAG, "traffic recorder", it) }
    }

    private fun startStats() {
        statsJob?.cancel()
        statsJob = scope.launch {
            var last: Pair<Long, Long>? = null
            var tick = 0
            val power = getSystemService(PowerManager::class.java)
            while (isActive) {
                // the speed, the graph and the session counter are shown only on
                // Vela's screen: while it is closed (or the screen is off) nothing
                // is asked every second. Forget the last sample so the next one
                // isn't a fake spike; the watchdog keeps «now using» fresh.
                if (!VpnManager.uiVisible.value) {
                    last = null
                    tick = 0
                    withTimeoutOrNull(60_000) { VpnManager.uiVisible.first { it } }
                    continue
                }
                delay(1000)
                if (!power.isInteractive) {
                    last = null
                    continue
                }
                val now = VpnManager.traffic() ?: continue
                val active = if (tick++ % 3 == 0) VpnManager.activeNode() else null
                val prev = last
                last = now
                if (VpnManager.status.value.state != ConnState.Connected) continue
                val up = if (prev != null) (now.first - prev.first).coerceAtLeast(0) else 0
                val down = if (prev != null) (now.second - prev.second).coerceAtLeast(0) else 0
                VpnManager.pushTraffic(up, down)
                // atomic: the watchdog changes the health at the same time
                whileConnected {
                    it.copy(up = up, down = down, upTotal = now.first, downTotal = now.second, activeNode = active ?: it.activeNode)
                }
            }
        }
    }

    /**
     * Checks that traffic really goes through: right after connecting (the
     * status says «checking» until then), every 30 seconds while the screen is
     * on, and at once after a network change, screen on or a server switch.
     * A failed check is confirmed within seconds; then a chosen server is
     * replaced by automatic selection (unless turned off) and automatic
     * selection re-tests its servers. When the network itself doesn't work no
     * server is blamed or replaced.
     */
    private fun startWatchdog() {
        watchJob?.cancel()
        // a poke left from before this start must not skip the first wait
        VpnManager.recheck.tryReceive()
        watchJob = scope.launch {
            val power = getSystemService(PowerManager::class.java)
            var fails = 0
            var offline = 0
            var verified = false
            var wait = 0L
            while (isActive) {
                if (wait > 0) {
                    val poke = withTimeoutOrNull(wait) { VpnManager.recheck.receive() }
                    if (poke != null && poke > 0) delay(poke)
                }
                if (VpnManager.status.value.state != ConnState.Connected) return@launch
                if (verified && !power.isInteractive) {
                    // the screen is off: nobody waits for an answer, the radio may sleep
                    wait = 120_000
                    continue
                }
                if (DefaultNetworkMonitor.defaultNetwork == null) {
                    // no network at all (lift, airplane mode): not the server's fault;
                    // a new network wakes this up
                    fails = 0
                    setHealth(Health.Offline)
                    wait = 60_000
                    continue
                }
                val ms = VpnManager.probeCurrent(if (verified) 6000 else 5000)
                if (VpnManager.status.value.state != ConnState.Connected) return@launch
                if (ms != null) {
                    fails = 0
                    offline = 0
                    verified = true
                    setHealth(Health.Ok)
                    noteWorking(ms)
                    wait = 30_000
                    continue
                }
                // just after a network change connections are being re-made: try again shortly
                if (DefaultNetworkMonitor.sinceChangeMs() < 8000) {
                    wait = 3000
                    continue
                }
                if (!VpnManager.directWorks()) {
                    // e.g. no internet behind this Wi-Fi yet: asked again soon at
                    // first, then calmly
                    fails = 0
                    setHealth(Health.Offline)
                    wait = if (offline++ < 5) 3000 else 15_000
                    continue
                }
                offline = 0
                fails++
                wait = when {
                    fails == 1 -> 2000
                    fails < 4 -> 5000
                    fails < 8 -> 15_000
                    else -> 30_000
                }
                // switched to automatic selection: its own failures count from zero
                if (failover(justConnected = !verified, fails = fails)) fails = 0
                // after a confirmed failure the screen-off rule applies as well:
                // no checks while nobody looks
                verified = verified || fails >= 2
            }
        }
    }

    private fun setHealth(health: Health) {
        val s = VpnManager.status.value
        if (s.state != ConnState.Connected || s.health == health) return
        whileConnected { it.copy(health = health) }
        showNotification(notificationText(health), update = true)
    }

    /**
     * The check went through the server in use: its result is the freshest one,
     * so the next start of automatic selection begins with it. The «now using»
     * line of the widget is kept current here while Vela's screen is closed.
     */
    private suspend fun noteWorking(ms: Int) {
        val id = VpnManager.activeNode() ?: return
        val s = VpnManager.status.value
        if (s.state == ConnState.Connected && s.activeNode != id) whileConnected { it.copy(activeNode = id) }
        val old = Store.pings.value[id] ?: 0
        // small changes aren't worth a write every 30 seconds
        if (old <= 0 || ms * 4 < old * 3 || ms * 3 > old * 4) {
            Store.setPings(mapOf(id to ms))
            Store.reasons.update { it - id }
            Store.savePings()
        }
    }

    /** Reacts to a failed check; true when it switched to automatic selection. */
    private suspend fun failover(justConnected: Boolean, fails: Int): Boolean {
        val st = Store.state.value
        val profile = st.active ?: return false
        val selected = st.selectedNode
        val manual = selected != "auto" && profile.nodes.any { it.id == selected }
        if (!manual) {
            // automatic selection: the one in use stopped answering, re-test now
            // (a server is only switched for a better one, so no flapping)
            if (fails == 2) markFailed(VpnManager.activeNode())
            if (fails == 1 || fails == 2 || fails % 4 == 0) VpnManager.retestAuto()
            if (fails >= 2) setHealth(Health.Failing)
            return false
        }
        if (!justConnected && fails < 2) return false
        if (st.settings.disableFailover) {
            setHealth(Health.Failing)
            if (justConnected && fails == 1) whileConnected { it.copy(error = Lang.str(R.string.error_server_silent_auto)) }
            return false
        }
        val name = profile.nodes.first { it.id == selected }.name
        // remembered as failed, so automatic selection skips it next time
        markFailed(selected)
        if (VpnManager.switchTo("auto")) {
            Store.select(profile.id, "auto")
            whileConnected { it.copy(error = null, notice = Lang.str(R.string.notice_switched_auto, name)) }
            return true
        }
        setHealth(Health.Failing)
        if (justConnected) whileConnected { it.copy(error = Lang.str(R.string.error_server_silent)) }
        return false
    }

    /** Changes the status only while connected: a «disconnect» meanwhile wins. */
    private inline fun whileConnected(crossinline change: (VpnStatus) -> VpnStatus) {
        VpnManager.status.update { if (it.state == ConnState.Connected) change(it) else it }
    }

    private fun markFailed(id: String?) {
        if (id.isNullOrEmpty()) return
        Store.setPings(mapOf(id to -1))
        Store.reasons.update { it + (id to Lang.str(R.string.reason_stopped_answering)) }
        Store.savePings()
    }

    private fun humanize(e: Exception): String {
        val msg = e.message ?: e.toString()
        return when {
            "missing vpn permission" in msg || "not prepared" in msg -> Lang.str(R.string.error_vpn_permission)
            e is VelaError -> msg
            else -> Lang.str(R.string.error_connect, msg)
        }
    }

    private fun freePort(): Int = java.net.ServerSocket(0, 1, java.net.InetAddress.getByName("127.0.0.1")).use { it.localPort }

    // ------------------------------------------------------------- notification

    private fun notificationText(health: Health): String = Lang.str(
        when (health) {
            Health.Ok -> R.string.notif_connected
            Health.Checking -> R.string.notif_checking
            Health.Offline -> R.string.status_offline
            Health.Failing -> R.string.status_failing
        },
    )

    /**
     * Shows the status in the foreground notification; false when Android refused it.
     * [update] only redraws the notification of the running service.
     */
    private fun showNotification(text: String, update: Boolean = false): Boolean {
        // redrawn only when the text changes (the watchdog reports every check)
        if (update && text == shownText) return true
        shownText = text
        val nm = getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= 26) {
            // also renames the channel after a language change
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL, Lang.str(R.string.notif_channel), NotificationManager.IMPORTANCE_LOW).apply {
                    setShowBadge(false)
                },
            )
        }
        val flags = PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), flags)
        val stop = PendingIntent.getService(this, 1, Intent(this, VelaVpnService::class.java).setAction(ACTION_STOP), flags)
        val profile = Store.state.value.active
        val notification: Notification = NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_vela)
            .setContentTitle(text)
            .setContentText(profile?.name ?: getString(R.string.app_name))
            .setContentIntent(open)
            .addAction(0, Lang.str(R.string.notif_disconnect), stop)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setShowWhen(false)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .build()
        if (update) return runCatching { nm.notify(NOTIFICATION_ID, notification) }.isSuccess
        val type = if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED else 0
        // Android 12+ may refuse a foreground start (from the background, or the
        // VPN permission went to another app): that must not crash the app
        return runCatching { ServiceCompat.startForeground(this, NOTIFICATION_ID, notification, type) }
            .onFailure { Log.e(TAG, "startForeground", it) }
            .isSuccess
    }

    private fun stopForegroundCompat() {
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
    }

    // ------------------------------------------------------------- CommandServerHandler

    override fun serviceStop() {
        val id = lastStartId
        post { stopVpn(id) }
    }

    override fun serviceReload() {
        // the same path as a settings change: under the lock, never racing a stop
        val id = lastStartId
        post { restartVpn(id) }
    }

    override fun getSystemProxyStatus(): SystemProxyStatus = SystemProxyStatus().apply {
        available = false
        enabled = false
    }

    override fun setSystemProxyEnabled(enabled: Boolean) {}

    override fun triggerNativeCrash() {
        throw UnsupportedOperationException()
    }

    override fun writeDebugMessage(message: String?) {
        Log.d(TAG, message ?: "")
    }

    override fun connectSSHAgent(): Int = throw UnsupportedOperationException()

    // ------------------------------------------------------------- PlatformInterface

    override fun usePlatformAutoDetectInterfaceControl(): Boolean = true

    /** Keeps the core's own sockets out of the tunnel (prevents loops). */
    override fun autoDetectInterfaceControl(fd: Int) {
        if (!protect(fd)) throw IllegalStateException("protect failed")
    }

    override fun openTun(options: TunOptions): Int {
        if (prepare(this) != null) throw IllegalStateException("android: missing vpn permission")
        val builder = Builder()
            .setSession(getString(R.string.app_name))
            .setMtu(options.mtu)
            .setConfigureIntent(
                PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE),
            )
        if (Build.VERSION.SDK_INT >= 29) builder.setMetered(false)

        // split tunneling by app
        val include = options.includePackage
        val exclude = options.excludePackage
        if (include.hasNext()) {
            // Vela itself stays inside so «My IP» and the speed test see the VPN
            runCatching { builder.addAllowedApplication(packageName) }
            while (include.hasNext()) {
                val pkg = include.next()
                runCatching { builder.addAllowedApplication(pkg) }
            }
        } else {
            while (exclude.hasNext()) {
                val pkg = exclude.next()
                if (pkg != packageName) runCatching { builder.addDisallowedApplication(pkg) }
            }
        }

        val v4 = options.inet4Address
        while (v4.hasNext()) v4.next().let { builder.addAddress(it.address(), it.prefix()) }
        val v6 = options.inet6Address
        while (v6.hasNext()) v6.next().let { builder.addAddress(it.address(), it.prefix()) }

        if (options.autoRoute) {
            if (options.dnsMode.value != Libbox.DNSModeDisabled) {
                val dns = options.dnsServerAddress
                while (dns.hasNext()) builder.addDnsServer(dns.next())
            }
            // route ranges already exclude the addresses listed in route_exclude_address
            val r4 = options.inet4RouteRange
            while (r4.hasNext()) r4.next().let { builder.addRoute(it.address(), it.prefix()) }
            val r6 = options.inet6RouteRange
            while (r6.hasNext()) r6.next().let { builder.addRoute(it.address(), it.prefix()) }
        }
        val pfd = builder.establish() ?: throw IllegalStateException("android: VPN is not prepared or was revoked")
        // after a reload the previous interface is replaced: release it
        val old = tunFd
        tunFd = pfd
        if (old != null) runCatching { old.close() }
        return pfd.fd
    }

    override fun useProcFS(): Boolean = Build.VERSION.SDK_INT < Build.VERSION_CODES.Q

    override fun findConnectionOwner(
        ipProtocol: Int, sourceAddress: String?, sourcePort: Int, destinationAddress: String?, destinationPort: Int,
    ): ConnectionOwner = throw UnsupportedOperationException("process rules are not used")

    override fun startDefaultInterfaceMonitor(listener: InterfaceUpdateListener?) {
        DefaultNetworkMonitor.setListener(listener)
    }

    override fun closeDefaultInterfaceMonitor(listener: InterfaceUpdateListener?) {
        DefaultNetworkMonitor.setListener(null)
    }

    override fun getInterfaces(): NetworkInterfaceIterator {
        val cm = getSystemService(android.net.ConnectivityManager::class.java)
        val javaIfaces = NetworkInterface.getNetworkInterfaces()?.toList().orEmpty()
        val result = mutableListOf<BoxInterface>()
        @Suppress("DEPRECATION")
        for (network in cm.allNetworks) {
            val props = cm.getLinkProperties(network) ?: continue
            val caps = cm.getNetworkCapabilities(network) ?: continue
            val iface = javaIfaces.find { it.name == props.interfaceName } ?: continue
            val box = BoxInterface()
            box.name = props.interfaceName
            box.index = iface.index
            runCatching { box.mtu = iface.mtu }
            box.dnsServer = StringArray(props.dnsServers.mapNotNull { it.hostAddress })
            box.gateway = StringArray(
                props.routes.filter { it.destination.prefixLength == 0 }
                    .mapNotNull { it.gateway }.filterNot { it.isAnyLocalAddress }.mapNotNull { it.hostAddress },
            )
            box.type = when {
                caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> Libbox.InterfaceTypeWIFI
                caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> Libbox.InterfaceTypeCellular
                caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> Libbox.InterfaceTypeEthernet
                else -> Libbox.InterfaceTypeOther
            }
            box.addresses = StringArray(iface.interfaceAddresses.map { it.toPrefix() })
            var flags = 0
            if (caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) flags = OsConstants.IFF_UP or OsConstants.IFF_RUNNING
            if (iface.isLoopback) flags = flags or OsConstants.IFF_LOOPBACK
            if (iface.isPointToPoint) flags = flags or OsConstants.IFF_POINTOPOINT
            if (iface.supportsMulticast()) flags = flags or OsConstants.IFF_MULTICAST
            box.flags = flags
            box.metered = !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
            result.add(box)
        }
        val it = result.iterator()
        return object : NetworkInterfaceIterator {
            override fun hasNext(): Boolean = it.hasNext()
            override fun next(): BoxInterface = it.next()
        }
    }

    override fun underNetworkExtension(): Boolean = false
    override fun includeAllNetworks(): Boolean = false
    override fun clearDNSCache() {}
    override fun readWIFIState(): WIFIState? = null
    override fun localDNSTransport(): LocalDNSTransport? = null
    override fun registerMyInterface(name: String?) {}
    override fun sendNotification(notification: BoxNotification?) {}
    override fun cancelNotification(identifier: String?, typeID: Int) {}
    override fun startNeighborMonitor(listener: NeighborUpdateListener?) {}
    override fun closeNeighborMonitor(listener: NeighborUpdateListener?) {}
    override fun usePlatformShell(): Boolean = false
    override fun checkPlatformShell() {
        throw UnsupportedOperationException()
    }
    override fun openShellSession(
        user: PlatformUser?, command: String?, environ: StringIterator?, term: String?, rows: Int, cols: Int,
    ): ShellSession = throw UnsupportedOperationException()
    override fun lookupUser(username: String?): PlatformUser = throw UnsupportedOperationException()
    override fun lookupSFTPServer(): String = throw UnsupportedOperationException()
    override fun readSystemSSHHostKey(): String = throw UnsupportedOperationException()
    override fun tailscaleHostname(): String = Build.MODEL
    override fun usePlatformBridge(): Boolean = false
    override fun createBridge(options: BridgeOptions?): BridgeSession = throw UnsupportedOperationException()

    private class StringArray(list: List<String>) : StringIterator {
        private val items = list
        private var i = 0
        override fun len(): Int = items.size
        override fun hasNext(): Boolean = i < items.size
        override fun next(): String = items[i++]
    }

    private fun InterfaceAddress.toPrefix(): String = if (address is Inet6Address) {
        "${Inet6Address.getByAddress(address.address).hostAddress}/$networkPrefixLength"
    } else {
        "${address.hostAddress}/$networkPrefixLength"
    }
}
