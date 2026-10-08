package app.vela.client.vpn

import android.content.Context
import android.content.Intent
import android.net.VpnService
import androidx.core.content.ContextCompat
import app.vela.client.data.Store
import app.vela.velacore.Velacore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.Proxy
import java.net.URL
import java.net.URLEncoder

enum class ConnState { Disconnected, Connecting, Connected, Disconnecting }

data class VpnStatus(
    val state: ConnState = ConnState.Disconnected,
    val error: String? = null,
    val since: Long = 0,
    val up: Long = 0,
    val down: Long = 0,
    val upTotal: Long = 0,
    val downTotal: Long = 0,
    /** Node id that currently carries traffic (resolves "auto"). */
    val activeNode: String? = null,
    /** Calm message, e.g. that Vela switched to another server. */
    val notice: String? = null,
)

/** What websites see: from MyIP in the Go core. */
data class IpInfo(val ip: String, val country: String, val city: String, val isp: String, val flag: String)

/** Speed test progress and result. */
data class SpeedState(
    val running: Boolean = false,
    val phase: String = "",
    val pingMs: Double = 0.0,
    val downMbps: Double = 0.0,
    val upMbps: Double = 0.0,
    val error: String? = null,
    val done: Boolean = false,
)

/** Shared between the VPN service and the UI (same process). */
object VpnManager {
    val status = MutableStateFlow(VpnStatus())

    /** Last two minutes of traffic: (upload, download) bytes per second. */
    val history = MutableStateFlow<List<Pair<Long, Long>>>(emptyList())

    val ip = MutableStateFlow<IpInfo?>(null)
    val ipBusy = MutableStateFlow(false)
    val ipError = MutableStateFlow<String?>(null)
    val speed = MutableStateFlow(SpeedState())

    const val TEST_URL = "https://www.gstatic.com/generate_204"

    @Volatile var controllerPort = 0
    @Volatile var controllerSecret = ""

    /** Returns an Intent to ask for VPN permission, or null when already granted. */
    fun prepare(context: Context): Intent? = VpnService.prepare(context)

    fun start(context: Context) {
        val intent = Intent(context, VelaVpnService::class.java).setAction(VelaVpnService.ACTION_START)
        ContextCompat.startForegroundService(context, intent)
    }

    fun stop(context: Context) {
        context.startService(Intent(context, VelaVpnService::class.java).setAction(VelaVpnService.ACTION_STOP))
    }

    // ------------------------------------------------------------ controller

    private fun request(method: String, path: String, body: String? = null, timeoutMs: Int = 8000): String? {
        val port = controllerPort
        if (port == 0) return null
        val conn = URL("http://127.0.0.1:$port$path").openConnection(Proxy.NO_PROXY) as HttpURLConnection
        return try {
            conn.requestMethod = method
            conn.connectTimeout = 2000
            conn.readTimeout = timeoutMs
            conn.setRequestProperty("Authorization", "Bearer $controllerSecret")
            if (body != null) {
                conn.doOutput = true
                conn.setRequestProperty("Content-Type", "application/json")
                conn.outputStream.use { it.write(body.toByteArray()) }
            }
            if (conn.responseCode >= 300) null else conn.inputStream.bufferedReader().use { it.readText() }
        } catch (_: Exception) {
            null
        } finally {
            conn.disconnect()
        }
    }

    /** Switches the server instantly without reconnecting. */
    suspend fun switchTo(nodeId: String): Boolean = withContext(Dispatchers.IO) {
        val target = if (nodeId == "auto") "auto" else Velacore.nodeTag(nodeId)
        request("PUT", "/proxies/proxy", JSONObject().put("name", target).toString()) != null
    }

    suspend fun traffic(): Pair<Long, Long>? = withContext(Dispatchers.IO) {
        val body = request("GET", "/connections") ?: return@withContext null
        val o = JSONObject(body)
        o.optLong("uploadTotal") to o.optLong("downloadTotal")
    }

    suspend fun activeNode(): String? = withContext(Dispatchers.IO) {
        var now = request("GET", "/proxies/proxy")?.let { JSONObject(it).optString("now") } ?: return@withContext null
        if (now == "auto") {
            now = request("GET", "/proxies/auto")?.let { JSONObject(it).optString("now") } ?: return@withContext null
        }
        now.removePrefix("node-")
    }

    /** Real request through the server in use; delay in ms or null. */
    suspend fun probeCurrent(): Int? = withContext(Dispatchers.IO) {
        val q = "url=" + URLEncoder.encode(TEST_URL, "UTF-8") + "&timeout=8000"
        request("GET", "/proxies/proxy/delay?$q", timeoutMs = 11000)?.let { JSONObject(it).optInt("delay", 0) }?.takeIf { it > 0 }
    }

    /** Makes automatic selection re-test its servers right now. */
    suspend fun retestAuto() = withContext(Dispatchers.IO) {
        val q = "url=" + URLEncoder.encode(TEST_URL, "UTF-8") + "&timeout=20000"
        request("GET", "/group/auto/delay?$q", timeoutMs = 25000)
    }

    fun pushTraffic(up: Long, down: Long) {
        history.update { (it + (up to down)).takeLast(120) }
    }

    /** Asks a website which IP and country it sees (through the VPN when on). */
    suspend fun refreshIp() {
        if (ipBusy.value) return
        ipBusy.value = true
        ipError.value = null
        try {
            withContext(Dispatchers.IO) {
                val o = JSONObject(Velacore.myIP())
                ip.value = IpInfo(
                    ip = o.optString("ip"), country = o.optString("country"), city = o.optString("city"),
                    isp = o.optString("isp"), flag = o.optString("flag"),
                )
            }
        } catch (e: Exception) {
            ip.value = null
            ipError.value = e.message ?: "не удалось узнать IP"
        } finally {
            ipBusy.value = false
        }
    }

    /** Measures ping, download and upload through the current connection. */
    suspend fun runSpeedTest() {
        if (speed.value.running) return
        speed.value = SpeedState(running = true, phase = "ping")
        val json = withContext(Dispatchers.IO) {
            Velacore.speedTest(8) { phase, value ->
                speed.update {
                    when (phase) {
                        "ping" -> it.copy(phase = "download", pingMs = value)
                        "download" -> it.copy(phase = "download", downMbps = value)
                        "upload" -> it.copy(phase = "upload", upMbps = value)
                        else -> it
                    }
                }
            }
        }
        val o = JSONObject(json)
        speed.value = SpeedState(
            running = false, done = true, phase = "done",
            pingMs = o.optDouble("pingMs", 0.0), downMbps = o.optDouble("downloadMbps", 0.0),
            upMbps = o.optDouble("uploadMbps", 0.0), error = o.optString("error").ifBlank { null },
        )
    }

    /** Progress of the server check: done to total, or null when idle. */
    val probe = MutableStateFlow<Pair<Int, Int>?>(null)

    /**
     * Makes a real request through every server of a profile. While the VPN is
     * off a temporary core does it and reports the reason for each failure;
     * while connected the running core is asked through its controller.
     */
    suspend fun pingAll(profileId: String? = null) = coroutineScope {
        if (probe.value != null) return@coroutineScope
        val st = Store.state.value
        val profile = st.profiles.firstOrNull { it.id == (profileId ?: st.activeProfile) } ?: st.active ?: return@coroutineScope
        val nodes = profile.nodes
        val connected = status.value.state == ConnState.Connected && profile.id == st.activeProfile
        Store.pings.update { it + nodes.associate { n -> n.id to 0 } }
        Store.reasons.update { it - nodes.map { n -> n.id }.toSet() }
        var done = 0
        probe.value = 0 to nodes.size
        fun report(id: String, ms: Int, reason: String) {
            Store.pings.update { it + (id to (if (ms > 0) ms else -1)) }
            if (ms <= 0 && reason.isNotEmpty()) Store.reasons.update { it + (id to reason) }
            synchronized(this@VpnManager) { done++ }
            probe.value = done to nodes.size
        }
        try {
            if (connected) {
                val gate = Semaphore(12)
                nodes.map { node ->
                    async(Dispatchers.IO) {
                        gate.withPermit {
                            val q = "url=" + URLEncoder.encode(TEST_URL, "UTF-8") + "&timeout=8000"
                            val ms = request("GET", "/proxies/${Velacore.nodeTag(node.id)}/delay?$q", timeoutMs = 11000)
                                ?.let { JSONObject(it).optInt("delay", -1) } ?: -1
                            report(node.id, ms, if (ms > 0) "" else "сервер не ответил")
                        }
                    }
                }.awaitAll()
            } else {
                withContext(Dispatchers.IO) {
                    val json = JSONArray().apply { nodes.forEach { put(it.raw) } }.toString()
                    val ok = runCatching {
                        Velacore.probeNodes(json, 9000, 12) { id, delay, reason, _ -> report(id, delay, reason) }
                    }
                    if (ok.isFailure) {
                        // temporary core unavailable: fall back to TCP reachability
                        nodes.forEach { n ->
                            if (n.isUdp) report(n.id, -1, "проверяется только после подключения")
                            else Velacore.tcpPing(n.server, n.port).let { report(n.id, it, if (it > 0) "" else "сервер недоступен") }
                        }
                    }
                }
            }
        } finally {
            probe.value = null
            Store.savePings()
        }
    }
}
