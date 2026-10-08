package app.vela.client.vpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
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
import app.vela.client.R
import app.vela.client.data.Store
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
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
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
        private const val CHANNEL = "vpn"
        private const val NOTIFICATION_ID = 1
        private const val TAG = "VelaVpn"
    }

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val lock = Mutex()
    private var commandServer: CommandServer? = null
    private var tunFd: ParcelFileDescriptor? = null
    private var statsJob: Job? = null

    // ------------------------------------------------------------- lifecycle

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> scope.launch { stopVpn() }
            // ACTION_START, or null/"android.net.VpnService" when started by Always-on VPN
            else -> {
                showNotification(getString(R.string.notif_connecting))
                scope.launch { startVpn() }
            }
        }
        return START_NOT_STICKY
    }

    override fun onRevoke() {
        // another VPN app took over, or the user revoked permission
        scope.launch { stopVpn() }
    }

    override fun onDestroy() {
        runCatching { commandServer?.close() }
        commandServer = null
        scope.cancel()
        super.onDestroy()
    }

    private suspend fun startVpn() = lock.withLock {
        val current = VpnManager.status.value.state
        if (current == ConnState.Connected) return@withLock
        VpnManager.status.value = VpnStatus(state = ConnState.Connecting)
        try {
            val port = freePort()
            val secret = Store.randomId(16)
            val config = Store.buildConfig(port, secret)
            VpnManager.controllerPort = port
            VpnManager.controllerSecret = secret
            DefaultNetworkMonitor.start()
            val server = commandServer ?: CommandServer(this, this).also {
                it.start()
                commandServer = it
            }
            server.startOrReloadService(config, OverrideOptions())
            Store.setWasConnected(true)
            VpnManager.status.value = VpnStatus(state = ConnState.Connected, since = System.currentTimeMillis() / 1000)
            showNotification(getString(R.string.notif_connected))
            startStats()
        } catch (e: Exception) {
            Log.e(TAG, "start failed", e)
            cleanup()
            VpnManager.status.value = VpnStatus(state = ConnState.Disconnected, error = humanize(e))
            stopForegroundCompat()
            stopSelf()
        }
    }

    private suspend fun stopVpn() = lock.withLock {
        VpnManager.status.value = VpnManager.status.value.copy(state = ConnState.Disconnecting)
        Store.setWasConnected(false)
        cleanup()
        VpnManager.status.value = VpnStatus(state = ConnState.Disconnected)
        stopForegroundCompat()
        stopSelf()
    }

    private fun cleanup() {
        statsJob?.cancel()
        statsJob = null
        runCatching { commandServer?.closeService() }
        runCatching { tunFd?.close() }
        tunFd = null
        DefaultNetworkMonitor.stop()
        VpnManager.controllerPort = 0
    }

    private fun startStats() {
        statsJob?.cancel()
        statsJob = scope.launch {
            var last: Pair<Long, Long>? = null
            var tick = 0
            val power = getSystemService(PowerManager::class.java)
            while (isActive) {
                delay(1000)
                // don't wake the radio just for statistics while the screen is off
                if (!power.isInteractive) continue
                val now = VpnManager.traffic() ?: continue
                val active = if (tick++ % 3 == 0) VpnManager.activeNode() else null
                val prev = last
                last = now
                val s = VpnManager.status.value
                if (s.state != ConnState.Connected) continue
                VpnManager.status.value = s.copy(
                    up = if (prev != null) (now.first - prev.first).coerceAtLeast(0) else 0,
                    down = if (prev != null) (now.second - prev.second).coerceAtLeast(0) else 0,
                    upTotal = now.first,
                    downTotal = now.second,
                    activeNode = active ?: s.activeNode,
                )
            }
        }
    }

    private fun humanize(e: Exception): String {
        val msg = e.message ?: e.toString()
        return when {
            "missing vpn permission" in msg -> "Нет разрешения на VPN. Нажмите «Подключить» ещё раз"
            "Сначала" in msg || "серверов" in msg -> msg
            else -> "Не удалось подключиться: $msg"
        }
    }

    private fun freePort(): Int = java.net.ServerSocket(0, 1, java.net.InetAddress.getByName("127.0.0.1")).use { it.localPort }

    // ------------------------------------------------------------- notification

    private fun showNotification(text: String) {
        val nm = getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= 26 && nm.getNotificationChannel(CHANNEL) == null) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL, getString(R.string.notif_channel), NotificationManager.IMPORTANCE_LOW).apply {
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
            .addAction(0, getString(R.string.notif_disconnect), stop)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setShowWhen(false)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .build()
        val type = if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED else 0
        ServiceCompat.startForeground(this, NOTIFICATION_ID, notification, type)
    }

    private fun stopForegroundCompat() {
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
    }

    // ------------------------------------------------------------- CommandServerHandler

    override fun serviceStop() {
        scope.launch { stopVpn() }
    }

    override fun serviceReload() {
        scope.launch {
            val port = VpnManager.controllerPort.takeIf { it != 0 } ?: freePort()
            val secret = VpnManager.controllerSecret.ifEmpty { Store.randomId(16) }
            runCatching { commandServer?.startOrReloadService(Store.buildConfig(port, secret), OverrideOptions()) }
        }
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
        tunFd = pfd
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
