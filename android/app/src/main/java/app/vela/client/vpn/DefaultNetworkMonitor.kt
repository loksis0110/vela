package app.vela.client.vpn

import android.annotation.SuppressLint
import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import android.os.SystemClock
import app.vela.libbox.InterfaceUpdateListener
import java.net.NetworkInterface

/**
 * Tracks the device's underlying default network (Wi-Fi / mobile) and
 * reports it to the core, so connections are re-established instantly when
 * the phone switches networks. Adapted from sing-box-for-android (GPL-3.0).
 */
@SuppressLint("StaticFieldLeak")
object DefaultNetworkMonitor {
    private lateinit var connectivity: ConnectivityManager
    @Volatile var defaultNetwork: Network? = null
        private set
    @Volatile private var listener: InterfaceUpdateListener? = null
    private var registered = false

    /** What the core was told last: capability updates that change nothing aren't passed on. */
    private var lastSent: Triple<String, Int, Boolean>? = null

    /** When the underlying network last changed (elapsedRealtime). */
    @Volatile private var changedAt = 0L

    /** Milliseconds since the underlying network last changed. */
    fun sinceChangeMs(): Long = SystemClock.elapsedRealtime() - changedAt

    private val handler by lazy { Handler(HandlerThread("vela-network").apply { start() }.looper) }

    private val request = NetworkRequest.Builder()
        .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
        .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_RESTRICTED)
        .build()

    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            val isNew = network != defaultNetwork
            defaultNetwork = network
            if (isNew) changed()
            notifyListener(force = isNew)
        }

        // fires on every bandwidth or signal estimate: only a real change
        // (another interface, metered or not) is passed to the core, which
        // otherwise re-reads every interface each time
        override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
            if (network == defaultNetwork) notifyListener()
        }

        override fun onLost(network: Network) {
            if (network == defaultNetwork) {
                defaultNetwork = null
                changed()
                notifyListener()
            }
        }
    }

    fun init(context: Context) {
        connectivity = context.getSystemService(ConnectivityManager::class.java)
    }

    @Synchronized
    fun start() {
        if (registered) return
        defaultNetwork = connectivity.activeNetwork
        // registerDefaultNetworkCallback would report our own VPN since Android 9,
        // so we request the best matching *underlying* network instead.
        when {
            Build.VERSION.SDK_INT >= 31 -> connectivity.registerBestMatchingNetworkCallback(request, callback, handler)
            Build.VERSION.SDK_INT >= 28 -> connectivity.requestNetwork(request, callback, handler)
            Build.VERSION.SDK_INT >= 26 -> connectivity.registerDefaultNetworkCallback(callback, handler)
            else -> connectivity.registerDefaultNetworkCallback(callback)
        }
        registered = true
    }

    @Synchronized
    fun stop() {
        if (!registered) return
        runCatching { connectivity.unregisterNetworkCallback(callback) }
        registered = false
        defaultNetwork = null
        synchronized(sentLock) { lastSent = null }
    }

    fun setListener(l: InterfaceUpdateListener?) {
        listener = l
        notifyListener(force = true)
    }

    /** A new network (or none): the connection check runs once the core has re-made its connections. */
    private fun changed() {
        changedAt = SystemClock.elapsedRealtime()
        VpnManager.recheckSoon(2000)
    }

    private val sentLock = Any()

    private fun send(l: InterfaceUpdateListener, name: String, index: Int, metered: Boolean, force: Boolean) {
        val key = Triple(name, index, metered)
        synchronized(sentLock) {
            if (!force && key == lastSent) return
            lastSent = key
        }
        // outside the lock: the core may call back into the app from here
        l.updateDefaultInterface(name, index, metered, false)
    }

    private fun notifyListener(force: Boolean = false) {
        val l = listener ?: return
        val network = defaultNetwork
        if (network == null) {
            send(l, "", -1, false, force)
            return
        }
        repeat(10) {
            val props = connectivity.getLinkProperties(network)
            val name = props?.interfaceName
            val index = runCatching { NetworkInterface.getByName(name).index }.getOrNull()
            if (name != null && index != null) {
                val caps = connectivity.getNetworkCapabilities(network)
                val metered = caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED) == false
                send(l, name, index, metered, force)
                return
            }
            Thread.sleep(100)
        }
    }
}
