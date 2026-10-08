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

    private val handler by lazy { Handler(HandlerThread("vela-network").apply { start() }.looper) }

    private val request = NetworkRequest.Builder()
        .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
        .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_RESTRICTED)
        .build()

    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            defaultNetwork = network
            notifyListener()
        }

        override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
            if (network == defaultNetwork) notifyListener()
        }

        override fun onLost(network: Network) {
            if (network == defaultNetwork) {
                defaultNetwork = null
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
    }

    fun setListener(l: InterfaceUpdateListener?) {
        listener = l
        notifyListener()
    }

    private fun notifyListener() {
        val l = listener ?: return
        val network = defaultNetwork
        if (network == null) {
            l.updateDefaultInterface("", -1, false, false)
            return
        }
        repeat(10) {
            val props = connectivity.getLinkProperties(network)
            val name = props?.interfaceName
            val index = runCatching { NetworkInterface.getByName(name).index }.getOrNull()
            if (name != null && index != null) {
                val caps = connectivity.getNetworkCapabilities(network)
                val metered = caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED) == false
                l.updateDefaultInterface(name, index, metered, false)
                return
            }
            Thread.sleep(100)
        }
    }
}
