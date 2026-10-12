package app.vela.client.vpn

import android.content.Context
import android.content.Intent
import android.net.VpnService
import androidx.core.content.ContextCompat
import app.vela.client.Lang
import app.vela.client.R
import app.vela.client.data.Node
import app.vela.client.data.Store
import app.vela.velacore.Velacore
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
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
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger

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
        // Android 12+ refuses foreground starts from the background
        runCatching { ContextCompat.startForegroundService(context, intent) }.onFailure {
            android.util.Log.e("VelaVpn", "start", it)
            if (status.value.state == ConnState.Disconnected) status.value = VpnStatus(error = Lang.str(R.string.error_connect, it.message ?: it.toString()))
        }
    }

    /** Rebuilds the config in the running service (settings, server list). */
    fun restart(context: Context) {
        if (status.value.state != ConnState.Connected) return
        runCatching { context.startService(Intent(context, VelaVpnService::class.java).setAction(VelaVpnService.ACTION_RESTART)) }
    }

    fun stop(context: Context) {
        runCatching { context.startService(Intent(context, VelaVpnService::class.java).setAction(VelaVpnService.ACTION_STOP)) }
            .onFailure { android.util.Log.e("VelaVpn", "stop", it) }
    }

    // ------------------------------------------------------------ controller

    private fun request(
        method: String, path: String, body: String? = null, timeoutMs: Int = 8000,
        tracked: MutableSet<HttpURLConnection>? = null,
    ): String? {
        val port = controllerPort
        if (port == 0) return null
        val conn = URL("http://127.0.0.1:$port$path").openConnection(Proxy.NO_PROXY) as HttpURLConnection
        tracked?.add(conn)
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
            tracked?.remove(conn)
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
        if (ipBusy.value) {
            // a check that started before (dis)connecting would show the old address
            ipAgain = true
            return
        }
        ipBusy.value = true
        ipError.value = null
        try {
            val info = withContext(Dispatchers.IO) {
                val o = JSONObject(Velacore.myIP())
                IpInfo(
                    ip = o.optString("ip"), country = o.optString("country"), city = o.optString("city"),
                    isp = o.optString("isp"), flag = o.optString("flag"),
                )
            }
            ip.value = info
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            ip.value = null
            ipError.value = e.message ?: Lang.str(R.string.ip_failed)
        } finally {
            ipBusy.value = false
        }
        if (ipAgain) {
            ipAgain = false
            refreshIp()
        }
    }

    @Volatile private var ipAgain = false

    /** Measures ping, download and upload through the current connection. */
    suspend fun runSpeedTest() {
        if (speed.value.running) return
        speed.value = SpeedState(running = true, phase = "ping")
        try {
            measureSpeed()
        } finally {
            // cancelled (screen rotated, app closed): never stay "running"
            if (speed.value.running) speed.update { it.copy(running = false) }
        }
    }

    private suspend fun measureSpeed() {
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

    @Volatile private var stopRequested = false

    /** Controller requests of a running check, closed by «Stop». */
    private val probeRequests: MutableSet<HttpURLConnection> = ConcurrentHashMap.newKeySet()

    /** Stops the server check; servers not checked yet keep no result. */
    fun stopProbe() {
        stopRequested = true
        runCatching { Velacore.stopProbe() }
        // requests through the running core would otherwise run to their timeout
        probeRequests.toList().forEach { runCatching { it.disconnect() } }
    }

    /** Delay of one server through the running core, or -1. */
    private fun probeThroughCore(nodeId: String, timeoutMs: Int, track: Boolean = false): Int {
        val q = "url=" + URLEncoder.encode(TEST_URL, "UTF-8") + "&timeout=$timeoutMs"
        return request(
            "GET", "/proxies/${Velacore.nodeTag(nodeId)}/delay?$q", timeoutMs = timeoutMs + 3000,
            tracked = if (track) probeRequests else null,
        )?.let { runCatching { JSONObject(it).optInt("delay", -1) }.getOrDefault(-1) } ?: -1
    }

    /** Checks one server (through the running core when it's connected). */
    suspend fun pingOne(profileId: String, nodeId: String) = withContext(Dispatchers.IO) {
        val st = Store.state.value
        val profile = st.profiles.firstOrNull { it.id == profileId } ?: return@withContext
        val node = profile.nodes.firstOrNull { it.id == nodeId } ?: return@withContext
        Store.pings.update { it + (node.id to 0) }
        Store.reasons.update { it - node.id }
        var ms = -1
        var reason = ""
        var got = true
        if (status.value.state == ConnState.Connected && profile.id == st.activeProfile) {
            ms = probeThroughCore(node.id, 8000)
            if (ms <= 0) reason = Lang.str(R.string.reason_no_answer)
        } else {
            val json = JSONArray().put(node.raw).toString()
            got = false
            runCatching {
                Velacore.probeNodes(json, 9000, 1) { _, delay, r, _ -> ms = delay; reason = r; got = true }
            }.onFailure { reason = Lang.str(R.string.reason_check_failed); got = true }
        }
        if (!got) {
            // stopped before an answer
            Store.pings.update { it - node.id }
            return@withContext
        }
        Store.pings.update { it + (node.id to (if (ms > 0) ms else -1)) }
        if (ms <= 0 && reason.isNotEmpty()) Store.reasons.update { it + (node.id to reason) }
        Store.savePings()
    }

    /**
     * Checks every server of a profile. Results arrive in bursts (hundreds
     * within a second when many hosts are dead), so they are collected and
     * shown a few times a second: the list stays smooth with 500+ servers.
     */
    suspend fun pingAll(profileId: String? = null): Unit = coroutineScope {
        if (probe.value != null) return@coroutineScope
        stopRequested = false
        val st = Store.state.value
        val profile = st.profiles.firstOrNull { it.id == (profileId ?: st.activeProfile) } ?: st.active ?: return@coroutineScope
        val nodes = profile.nodes
        if (nodes.isEmpty()) return@coroutineScope
        val connected = status.value.state == ConnState.Connected && profile.id == st.activeProfile
        val total = nodes.size
        Store.pings.update { it + nodes.associate { n -> n.id to 0 } }
        Store.reasons.update { it - nodes.map { n -> n.id }.toSet() }
        probe.value = 0 to total

        val pending = ArrayList<Triple<String, Int, String>>()
        val done = AtomicInteger(0)
        fun report(id: String, ms: Int, reason: String) {
            if (stopRequested && ms <= 0) return
            synchronized(pending) { pending += Triple(id, if (ms > 0) ms else -1, reason) }
        }
        fun flush() {
            val batch = synchronized(pending) {
                if (pending.isEmpty()) return
                ArrayList(pending).also { pending.clear() }
            }
            Store.pings.update { m -> HashMap(m).apply { batch.forEach { (id, ms, _) -> put(id, ms) } } }
            val failed = batch.filter { it.second <= 0 && it.third.isNotEmpty() }
            if (failed.isNotEmpty()) Store.reasons.update { m -> HashMap(m).apply { failed.forEach { (id, _, r) -> put(id, r) } } }
            probe.value = done.addAndGet(batch.size).coerceAtMost(total) to total
        }
        val ticker = launch {
            while (true) {
                delay(150)
                flush()
            }
        }
        try {
            withContext(Dispatchers.IO) {
                if (connected) probeConnected(nodes, ::report) else probeOffline(nodes, ::report)
            }
        } finally {
            ticker.cancel()
            flush()
            // stopped: servers that weren't checked must not stay "measuring"
            val ids = nodes.map { it.id }.toSet()
            Store.pings.update { m -> m.filterNot { it.key in ids && it.value == 0 } }
            probe.value = null
            Store.savePings()
        }
    }

    /** Checks with a temporary core; the core itself spreads the work sensibly. */
    private suspend fun probeOffline(nodes: List<Node>, report: (String, Int, String) -> Unit): Unit = coroutineScope {
        val json = JSONArray().apply { nodes.forEach { put(it.raw) } }.toString()
        val ok = runCatching {
            Velacore.probeNodes(json, 9000, 12) { id, delay, reason, _ -> report(id, delay, reason) }
        }
        if (ok.isSuccess || stopRequested) return@coroutineScope
        // temporary core unavailable: fall back to TCP reachability
        val gate = Semaphore(16)
        nodes.map { n ->
            async {
                gate.withPermit {
                    if (stopRequested) return@withPermit
                    if (n.isUdp) {
                        report(n.id, -1, Lang.str(R.string.reason_only_connected))
                    } else {
                        val ms = Velacore.tcpPing(n.server, n.port)
                        report(n.id, ms, if (ms > 0) "" else Lang.str(R.string.reason_unreachable))
                    }
                }
            }
        }.awaitAll()
    }

    /**
     * Checks through the running core (its controller), the way the core does it
     * for a temporary one: at most 3 checks per server address at a time and 24
     * in all; an address that failed 3 times without a single answer isn't
     * waited for again and again.
     */
    private suspend fun probeConnected(nodes: List<Node>, report: (String, Int, String) -> Unit): Unit = coroutineScope {
        class Endpoint {
            val gate = Semaphore(3)
            val fails = AtomicInteger(0)
            val oks = AtomicInteger(0)
        }
        val all = Semaphore(24)
        val endpoints = HashMap<String, Endpoint>()
        val noAnswer = Lang.str(R.string.reason_no_answer)
        nodes.map { node ->
            val ep = endpoints.getOrPut("${node.server}:${node.port}") { Endpoint() }
            async {
                ep.gate.withPermit {
                    all.withPermit {
                        if (stopRequested) return@withPermit
                        if (ep.fails.get() >= 3 && ep.oks.get() == 0) {
                            report(node.id, -1, noAnswer)
                            return@withPermit
                        }
                        val ms = probeThroughCore(node.id, 6000, track = true)
                        if (ms > 0) ep.oks.incrementAndGet() else if (!stopRequested) ep.fails.incrementAndGet()
                        report(node.id, ms, if (ms > 0) "" else noAnswer)
                    }
                }
            }
        }.awaitAll()
    }
}
