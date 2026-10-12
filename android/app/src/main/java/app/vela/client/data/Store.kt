package app.vela.client.data

import android.content.Context
import android.content.SharedPreferences
import android.net.Uri
import app.vela.client.BuildConfig
import app.vela.client.Lang
import app.vela.client.R
import app.vela.client.notify.Reminders
import app.vela.client.vpn.VpnManager
import app.vela.velacore.Velacore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import android.util.Log
import java.io.File
import java.io.FileOutputStream
import java.security.SecureRandom
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

data class Node(
    val id: String,
    val name: String,
    val protocol: String,
    val server: String,
    val port: Int,
    val security: String,
    val network: String,
    /** "xray" for servers run by the embedded Xray engine. */
    val engine: String,
    /** Full node object as produced by the Go core (kept verbatim). */
    val raw: JSONObject,
) {
    val isUdp: Boolean get() = protocol == "Hysteria2" || protocol == "TUIC"

    companion object {
        fun from(o: JSONObject) = Node(
            id = o.optString("id"),
            name = o.optString("name"),
            protocol = o.optString("protocol"),
            server = o.optString("server"),
            port = o.optInt("port"),
            security = o.optString("security"),
            network = o.optString("network"),
            engine = o.optString("engine"),
            raw = o,
        )
    }
}

data class Profile(
    val id: String,
    val name: String,
    val kind: String, // "subscription" | "manual"
    val title: String = "",
    val customName: String = "",
    val url: String = "",
    val nodes: List<Node> = emptyList(),
    val upload: Long = 0,
    val download: Long = 0,
    val total: Long = 0,
    val expire: Long = 0,
    val intervalHours: Int = 0,
    val supportUrl: String = "",
    val updatedAt: Long = 0,
    /** Message from the provider, shown as a banner. */
    val announce: String = "",
) {
    fun toJson(): JSONObject = JSONObject()
        .put("id", id).put("name", name).put("kind", kind).put("url", url)
        .put("title", title).put("customName", customName)
        .put("nodes", JSONArray().apply { nodes.forEach { put(it.raw) } })
        .put("upload", upload).put("download", download).put("total", total)
        .put("expire", expire).put("updateIntervalHours", intervalHours)
        .put("supportUrl", supportUrl).put("updatedAt", updatedAt)
        .put("announce", announce)

    companion object {
        fun from(o: JSONObject) = Profile(
            id = o.optString("id"),
            name = o.optString("name"),
            kind = o.optString("kind"),
            title = o.optString("title"),
            customName = o.optString("customName"),
            url = o.optString("url"),
            nodes = o.optJSONArray("nodes").toNodes(),
            upload = o.optLong("upload"),
            download = o.optLong("download"),
            total = o.optLong("total"),
            expire = o.optLong("expire"),
            intervalHours = o.optInt("updateIntervalHours"),
            supportUrl = o.optString("supportUrl"),
            updatedAt = o.optLong("updatedAt"),
            announce = o.optString("announce"),
        )
    }
}

private fun JSONArray?.toNodes(): List<Node> {
    if (this == null) return emptyList()
    // the same server twice breaks lists (duplicate keys) and the config
    return (0 until length()).mapNotNull { optJSONObject(it)?.let(Node::from) }
        .filter { it.id.isNotEmpty() }.distinctBy { it.id }
}

/** User routing rule: domain, *keyword* or IP/CIDR → direct, proxy or block. */
data class Rule(val match: String, val action: String = "direct") {
    fun toJson(): JSONObject = JSONObject().put("match", match).put("action", action)
}

data class Settings(
    val routing: String = "smart",
    val blockAds: Boolean = false,
    val ipv6: Boolean = true,
    val stack: String = "mixed",
    val remoteDns: String = "https://1.1.1.1/dns-query",
    val directDns: String = "77.88.8.8",
    val directDomains: List<String> = emptyList(),
    val proxyDomains: List<String> = emptyList(),
    /** Switch to a working server when the chosen one stops answering. */
    val disableFailover: Boolean = false,
    /** "" off, "record" light, "full" strongest TLS handshake splitting. */
    val tlsFragment: String = "",
    val rules: List<Rule> = emptyList(),
    /** "" all apps, "exclude" listed apps bypass the VPN, "only" only listed apps use it. */
    val appMode: String = "",
    val apps: List<String> = emptyList(),
) {
    fun toJson(): JSONObject = JSONObject()
        // Google Play's VpnService policy forbids ad blocking
        .put("mode", "tun").put("routing", routing).put("blockAds", blockAds && !BuildConfig.PLAY)
        .put("ipv6", ipv6).put("stack", stack).put("remoteDns", remoteDns)
        .put("directDns", directDns)
        .put("directDomains", JSONArray(directDomains))
        .put("proxyDomains", JSONArray(proxyDomains))
        .put("logLevel", "warn")
        .put("disableFailover", disableFailover)
        .put("tlsFragment", tlsFragment)
        .put("rules", JSONArray().apply { rules.forEach { put(it.toJson()) } })
        .put("appMode", appMode)
        .put("apps", JSONArray(apps))

    companion object {
        fun from(o: JSONObject?): Settings {
            if (o == null) return Settings()
            return Settings(
                routing = o.optString("routing", "smart"),
                blockAds = o.optBoolean("blockAds"),
                ipv6 = o.optBoolean("ipv6", true),
                stack = o.optString("stack", "mixed"),
                remoteDns = o.optString("remoteDns", "https://1.1.1.1/dns-query"),
                directDns = o.optString("directDns", "77.88.8.8"),
                directDomains = o.optJSONArray("directDomains").strings(),
                proxyDomains = o.optJSONArray("proxyDomains").strings(),
                disableFailover = o.optBoolean("disableFailover"),
                tlsFragment = o.optString("tlsFragment"),
                rules = o.optJSONArray("rules")?.let { arr ->
                    (0 until arr.length()).mapNotNull { i ->
                        arr.optJSONObject(i)?.let { r -> Rule(r.optString("match"), r.optString("action", "direct")) }
                    }.filter { it.match.isNotBlank() }
                } ?: emptyList(),
                appMode = o.optString("appMode"),
                apps = o.optJSONArray("apps").strings(),
            )
        }
    }
}

private fun JSONArray?.strings(): List<String> =
    if (this == null) emptyList() else (0 until length()).mapNotNull { i -> opt(i)?.takeIf { it != JSONObject.NULL }?.toString() }

/** Subscription settings. Defaults: auto update as the provider says, HWID on. */
data class SubPrefs(
    val noAutoUpdate: Boolean = false,
    /** 0: as the provider says (12 h if it doesn't). */
    val updateHours: Int = 0,
    val noHwid: Boolean = false,
    val userAgent: String = "",
) {
    fun toJson(): JSONObject = JSONObject().put("noAutoUpdate", noAutoUpdate).put("updateHours", updateHours)
        .put("noHwid", noHwid).put("userAgent", userAgent)

    companion object {
        fun from(o: JSONObject?) = if (o == null) SubPrefs() else SubPrefs(
            noAutoUpdate = o.optBoolean("noAutoUpdate"), updateHours = o.optInt("updateHours"),
            noHwid = o.optBoolean("noHwid"), userAgent = o.optString("userAgent"),
        )
    }
}

data class AppState(
    val profiles: List<Profile> = emptyList(),
    val activeProfile: String = "",
    val selectedNode: String = "auto",
    val settings: Settings = Settings(),
    val wasConnected: Boolean = false,
    /** Favorite server ids; automatic selection prefers them. */
    val favorites: Set<String> = emptySet(),
    /** "system", "light" or "dark". */
    val theme: String = "system",
    val sub: SubPrefs = SubPrefs(),
    /** Random id of this installation, sent as HWID when enabled. */
    val hwid: String = "",
    val connectOnLaunch: Boolean = false,
    /** Servers sorted by the last check. */
    val sortByPing: Boolean = false,
    /** Don't offer links found in the clipboard. */
    val noClipboard: Boolean = false,
    /** Don't remind about the end of subscriptions and traffic. */
    val noReminders: Boolean = false,
) {
    val active: Profile? get() = profiles.firstOrNull { it.id == activeProfile } ?: profiles.firstOrNull()
}

/** A problem the user can fix; the message is shown as is. */
class VelaError(message: String) : Exception(message)

private fun fail(id: Int, vararg args: Any): Nothing = throw VelaError(Lang.str(id, *args))

/** Holds the app state, persisted as JSON in the app's private storage. */
object Store {
    private lateinit var file: File
    lateinit var dataDir: File
        private set
    private lateinit var appContext: Context

    /** Small things that are not settings: dismissed banners, clipboard, alerts shown. */
    lateinit var prefs: SharedPreferences
        private set

    private val _state = MutableStateFlow(AppState())
    val state: StateFlow<AppState> = _state.asStateFlow()

    /** Latency in ms per node id: -1 unreachable, -2 measured only when connected, 0 measuring. */
    val pings = MutableStateFlow<Map<String, Int>>(emptyMap())

    /** Why a server failed the last check (human readable). */
    val reasons = MutableStateFlow<Map<String, String>>(emptyMap())

    /** Hidden provider announcements: profile id to the hash of the hidden text. */
    val dismissedAnnounce = MutableStateFlow<Map<String, String>>(emptyMap())

    const val MANUAL_ID = "manual"

    fun init(context: Context) {
        appContext = context.applicationContext ?: context
        dataDir = context.filesDir
        file = File(dataDir, "state.json")
        prefs = context.getSharedPreferences("ui", Context.MODE_PRIVATE)
        _state.value = load()
        loadPings()
        runCatching {
            val o = JSONObject(prefs.getString("dismissedAnnounce", "{}") ?: "{}")
            dismissedAnnounce.value = o.keys().asSequence().associateWith { o.getString(it) }
        }
        if (_state.value.hwid.isEmpty()) update { it.copy(hwid = randomId(16)) }
        relabel()
        applyFetchOptions()
    }

    /**
     * «My servers» is named in the app language (1.3 stored the Russian name);
     * a name the user gave it stays.
     */
    fun relabel() {
        val label = Lang.str(R.string.my_servers)
        val manual = _state.value.profiles.firstOrNull { it.id == MANUAL_ID } ?: return
        if (manual.customName.isNotBlank() || manual.name == label) return
        update { st -> st.copy(profiles = st.profiles.map { if (it.id == MANUAL_ID && it.customName.isBlank()) it.copy(name = label) else it }) }
    }

    /** Statistics file written by the traffic recorder of the core. */
    val trafficFile: File get() = File(dataDir, "traffic.json")

    /** Passes the subscription settings to the core. */
    private fun applyFetchOptions() {
        val st = _state.value
        val o = JSONObject().put("userAgent", st.sub.userAgent)
        if (!st.sub.noHwid) {
            o.put("hwid", st.hwid).put("deviceOs", "Android")
                .put("osVersion", android.os.Build.VERSION.RELEASE ?: "")
                .put("deviceModel", android.os.Build.MODEL ?: "")
        }
        runCatching { Velacore.setSubscriptionOptions(o.toString()) }
    }

    fun setSubPrefs(p: SubPrefs) {
        update { it.copy(sub = p.copy(userAgent = p.userAgent.trim(), updateHours = p.updateHours.coerceIn(0, 168))) }
        applyFetchOptions()
    }

    fun setConnectOnLaunch(on: Boolean) = update { it.copy(connectOnLaunch = on) }

    fun setSortByPing(on: Boolean) = update { it.copy(sortByPing = on) }

    fun setClipboard(on: Boolean) = update { it.copy(noClipboard = !on) }

    fun setReminders(on: Boolean) {
        update { it.copy(noReminders = !on) }
        Reminders.onChanged(appContext)
    }

    /** Settings back to defaults; subscriptions, servers and favorites stay. */
    fun resetSettings() {
        update {
            it.copy(
                settings = Settings(), theme = "system", sub = SubPrefs(), connectOnLaunch = false,
                noClipboard = false, noReminders = false,
            )
        }
        applyFetchOptions()
        Reminders.onChanged(appContext)
    }

    /** Hides the provider's message until it changes. */
    fun dismissAnnounce(profileId: String, text: String) {
        val next = dismissedAnnounce.value + (profileId to announceHash(text))
        dismissedAnnounce.value = next
        prefs.edit().putString("dismissedAnnounce", JSONObject(next as Map<*, *>).toString()).apply()
    }

    fun announceHash(text: String): String = text.trim().hashCode().toString(16)

    /** The announcement to show for a profile, or null when there is none or it was hidden. */
    fun visibleAnnounce(p: Profile?, dismissed: Map<String, String>): String? {
        val text = p?.announce?.trim().orEmpty()
        if (text.isEmpty() || dismissed[p!!.id] == announceHash(text)) return null
        return text
    }

    /** Core log of the current connection (shown in Settings). */
    val logFile: File get() = File(dataDir, "core.log")

    private fun load(): AppState {
        val main = runCatching { parse(JSONObject(file.readText())) }
        main.getOrNull()?.let { return it }
        if (!file.exists()) return AppState()
        // never overwrite what can't be read: keep it for recovery, then try the
        // copy a write may have left behind
        Log.w("VelaStore", "state.json is unreadable", main.exceptionOrNull())
        runCatching { file.copyTo(File(dataDir, "state.json.bad"), overwrite = true) }
        return runCatching { parse(JSONObject(File(dataDir, "state.json.tmp").readText())) }.getOrDefault(AppState())
    }

    private fun parse(o: JSONObject) = AppState(
        // one broken entry must not cost the whole list
        profiles = o.optJSONArray("profiles")?.let { arr ->
            (0 until arr.length()).mapNotNull { i -> arr.optJSONObject(i)?.let { runCatching { Profile.from(it) }.getOrNull() } }
                .filter { it.id.isNotEmpty() }.distinctBy { it.id }
        } ?: emptyList(),
        activeProfile = o.optString("activeProfile"),
        selectedNode = o.optString("selectedNode", "auto").ifEmpty { "auto" },
        settings = runCatching { Settings.from(o.optJSONObject("settings")) }.getOrDefault(Settings()),
        wasConnected = o.optBoolean("wasConnected"),
        favorites = o.optJSONArray("favorites").strings().toSet(),
        theme = o.optString("theme", "system"),
        sub = runCatching { SubPrefs.from(o.optJSONObject("sub")) }.getOrDefault(SubPrefs()),
        hwid = o.optString("hwid"),
        connectOnLaunch = o.optBoolean("connectOnLaunch"),
        sortByPing = o.optBoolean("sortByPing"),
        noClipboard = o.optBoolean("noClipboard"),
        noReminders = o.optBoolean("noReminders"),
    )

    private fun loadPings() {
        runCatching {
            val o = JSONObject(File(dataDir, "pings.json").readText())
            pings.value = o.keys().asSequence().associateWith { o.getInt(it) }.filterValues { it != 0 }
        }
    }

    /** Remembers check results so automatic selection can skip dead servers after a restart. */
    fun savePings() {
        // written by the same thread as the state, never on the UI thread
        if (pingsQueued.compareAndSet(false, true)) {
            writer.execute {
                pingsQueued.set(false)
                runCatching {
                    val o = JSONObject()
                    pings.value.filterValues { it != 0 }.forEach { (k, v) -> o.put(k, v) }
                    writeAtomically(File(dataDir, "pings.json"), o.toString())
                }.onFailure { Log.w("VelaStore", "can't save the check results", it) }
            }
        }
    }

    private val pingsQueued = AtomicBoolean(false)

    /**
     * Waits until every change made so far is on disk. Called when the app
     * leaves the screen: Android may kill the process at any moment after that.
     */
    fun flush() {
        runCatching { writer.submit(Runnable {}).get(3, TimeUnit.SECONDS) }
    }

    /** Replaces the file only with fully written data (survives a crash or power loss). */
    private fun writeAtomically(target: File, text: String) {
        val tmp = File(target.parentFile, target.name + ".tmp")
        FileOutputStream(tmp).use { out ->
            out.write(text.toByteArray())
            out.flush()
            runCatching { out.fd.sync() }
        }
        if (!tmp.renameTo(target)) {
            tmp.copyTo(target, overwrite = true)
            tmp.delete()
        }
    }

    @Synchronized
    private fun update(block: (AppState) -> AppState) {
        val next = block(_state.value)
        if (next == _state.value) return
        _state.value = next
        // written off the calling thread (often the UI); bursts of changes are
        // coalesced and the newest state always ends up on disk
        if (writeQueued.compareAndSet(false, true)) {
            writer.execute {
                writeQueued.set(false)
                write(_state.value)
            }
        }
    }

    private val writer = Executors.newSingleThreadExecutor { r -> Thread(r, "vela-store").apply { isDaemon = true } }
    private val writeQueued = AtomicBoolean(false)

    private fun write(st: AppState) {
        try {
            val o = JSONObject()
                .put("profiles", JSONArray().apply { st.profiles.forEach { put(it.toJson()) } })
                .put("activeProfile", st.activeProfile)
                .put("selectedNode", st.selectedNode)
                .put("settings", st.settings.toJson())
                .put("wasConnected", st.wasConnected)
                .put("favorites", JSONArray(st.favorites.toList()))
                .put("theme", st.theme)
                .put("sub", st.sub.toJson())
                .put("hwid", st.hwid)
                .put("connectOnLaunch", st.connectOnLaunch)
                .put("sortByPing", st.sortByPing)
                .put("noClipboard", st.noClipboard)
                .put("noReminders", st.noReminders)
            writeAtomically(file, o.toString())
        } catch (e: Exception) {
            // full storage and the like: the app keeps working with what is in memory
            Log.e("VelaStore", "can't save the state", e)
        }
    }

    /** True when this subscription URL is already added. */
    fun hasSubscription(url: String): Boolean =
        _state.value.profiles.any { it.kind == "subscription" && it.url.trim() == url.trim() }

    /** True when every share link in the text is already among «My servers». */
    fun hasAllLinks(links: String): Boolean {
        val known = _state.value.profiles.flatMap { it.nodes }.map { it.raw.optString("link").trim() }.toSet()
        val list = links.lines().map { it.trim() }.filter { it.isNotEmpty() }
        return list.isNotEmpty() && list.all { it in known }
    }

    /** Downloads a subscription without adding it (for the confirmation of an app link). */
    suspend fun preview(url: String): JSONObject = withContext(Dispatchers.IO) {
        JSONObject(Velacore.fetchSubscription(url))
    }

    /** Adds a subscription downloaded by [preview]. Returns a message for the user. */
    suspend fun addFetched(url: String, sub: JSONObject, name: String = ""): String = withContext(Dispatchers.IO) {
        val (label, isNew) = saveSubscription(url, sub)
        val id = _state.value.profiles.firstOrNull { it.kind == "subscription" && it.url == url }?.id
        if (id != null) {
            makeActiveIfNone(id)
            applySuggestedName(id, name)
        }
        Reminders.onSubscriptionsChanged(appContext)
        val count = sub.optJSONArray("nodes").toNodes().size
        if (isNew) Lang.str(R.string.import_sub_added, label, count) else Lang.str(R.string.import_sub_updated, label)
    }

    /** Uses the name from an app link when the provider gives none. */
    fun applySuggestedName(profileId: String, name: String) {
        val p = _state.value.profiles.firstOrNull { it.id == profileId } ?: return
        if (name.isNotBlank() && p.title.isBlank() && p.customName.isBlank()) rename(p.id, name.take(60))
    }

    private fun saveSubscription(url: String, sub: JSONObject): Pair<String, Boolean> {
        val nodes = sub.optJSONArray("nodes").toNodes()
        var label = ""
        var isNew = false
        var reload = false
        update { st ->
            val existing = st.profiles.firstOrNull { it.kind == "subscription" && it.url == url }
            isNew = existing == null
            var profile = subscriptionProfile(existing?.id ?: randomId(), url, sub, nodes, existing?.customName ?: "")
            profile = profile.copy(name = uniqueName(st.profiles, profile))
            label = profile.name
            val profiles = if (existing != null) st.profiles.map { if (it.id == existing.id) profile else it } else st.profiles + profile
            val next = st.copy(profiles = profiles)
            if (existing != null) withNewServers(st, next, existing.id).also { reload = it.second }.first else next
        }
        if (reload) VpnManager.restart(appContext)
        return label to isNew
    }

    /**
     * After the servers of a subscription were replaced: when it is the one in
     * use and its server set really changed, the running VPN must get the new
     * list (returned as true). The chosen server stays chosen while it exists.
     */
    private fun withNewServers(before: AppState, after: AppState, profileId: String): Pair<AppState, Boolean> {
        if (before.active?.id != profileId) return after to false
        val old = before.active?.nodes.orEmpty().map { it.id }.toSet()
        val fresh = after.profiles.firstOrNull { it.id == profileId }?.nodes.orEmpty().map { it.id }.toSet()
        if (old == fresh) return after to false
        val selected = if (after.selectedNode == "auto" || after.selectedNode in fresh) after.selectedNode else "auto"
        return after.copy(selectedNode = selected) to true
    }

    private fun makeActiveIfNone(target: String) = update { st ->
        // the first import becomes active; later ones are added alongside
        if (st.activeProfile.isEmpty() || st.profiles.none { it.id == st.activeProfile }) {
            st.copy(activeProfile = target, selectedNode = "auto")
        } else {
            st
        }
    }

    /**
     * Imports any mix of subscription URLs, share links and base64 bodies.
     * Several subscriptions can be pasted at once, one per line.
     */
    suspend fun import(text: String): String = withContext(Dispatchers.IO) {
        val input = text.trim()
        if (input.isEmpty()) fail(R.string.import_empty)
        val backup = runCatching { Velacore.decodeBackup(input) }.getOrElse { throw VelaError(it.message ?: Lang.str(R.string.backup_broken)) }
        if (backup.isNotEmpty()) return@withContext restore(JSONObject(backup))
        val split = JSONObject(Velacore.splitImport(input))
        val urls = split.optJSONArray("subscriptions").strings()
        val linksText = split.optString("links")
        val parts = mutableListOf<String>()
        val failures = mutableListOf<String>()
        var lastProfile: String? = null

        val fetched = coroutineScope {
            urls.map { url -> async { url to runCatching { JSONObject(Velacore.fetchSubscription(url)) } } }.awaitAll()
        }
        for ((url, result) in fetched) {
            val sub = result.getOrElse {
                failures += (Uri.parse(url).host ?: url) + ": " + (it.message ?: Lang.str(R.string.error_generic))
                null
            } ?: continue
            val (label, isNew) = saveSubscription(url, sub)
            lastProfile = _state.value.profiles.firstOrNull { it.kind == "subscription" && it.url == url }?.id ?: lastProfile
            val count = sub.optJSONArray("nodes").toNodes().size
            parts += if (isNew) Lang.str(R.string.import_sub_added, label, count) else Lang.str(R.string.import_sub_updated, label)
        }

        if (linksText.isNotBlank()) {
            val parsed = JSONObject(Velacore.parseText(linksText))
            val nodes = parsed.optJSONArray("nodes").toNodes()
            val errors = parsed.optJSONArray("errors").strings()
            if (nodes.isEmpty()) {
                failures += errors.firstOrNull() ?: Lang.str(R.string.import_no_links_lower)
            } else {
                var added = 0
                update { st ->
                    val manual = st.profiles.firstOrNull { it.id == MANUAL_ID } ?: Profile(MANUAL_ID, Lang.str(R.string.my_servers), "manual")
                    val known = manual.nodes.map { it.raw.optString("link") }.toSet()
                    val fresh = nodes.filter { it.raw.optString("link") !in known && manual.nodes.none { m -> m.id == it.id } }
                    added = fresh.size
                    val updated = manual.copy(nodes = manual.nodes + fresh)
                    val profiles = if (st.profiles.any { it.id == MANUAL_ID }) {
                        st.profiles.map { if (it.id == MANUAL_ID) updated else it }
                    } else {
                        listOf(updated) + st.profiles
                    }
                    st.copy(profiles = profiles)
                }
                lastProfile = MANUAL_ID
                parts += if (added == 0) Lang.str(R.string.import_links_known) else Lang.str(R.string.import_links_added, added)
                if (errors.isNotEmpty()) parts += Lang.str(R.string.import_links_skipped, errors.size)
            }
        }

        val target = lastProfile ?: throw VelaError(failures.joinToString("; ").ifEmpty { Lang.str(R.string.import_no_links) })
        makeActiveIfNone(target)
        if (urls.isNotEmpty()) Reminders.onSubscriptionsChanged(appContext)
        var msg = parts.joinToString(", ")
        if (failures.isNotEmpty()) msg += ". " + Lang.str(R.string.import_failed, failures.joinToString("; "))
        msg.replaceFirstChar { it.uppercase() }
    }

    /** Keeps profile names unique so tabs can be told apart. */
    private fun uniqueName(all: List<Profile>, p: Profile): String {
        if (p.customName.isNotBlank()) return p.customName
        val base = p.name.ifBlank { Uri.parse(p.url).host ?: Lang.str(R.string.subscription) }
        var name = base
        var n = 2
        while (all.any { it.id != p.id && it.name == name }) {
            name = "$base $n"
            n++
        }
        return name
    }

    fun rename(profileId: String, name: String) = update { st ->
        st.copy(
            profiles = st.profiles.map { p ->
                if (p.id != profileId) {
                    p
                } else {
                    val custom = name.trim()
                    val renamed = p.copy(customName = custom, name = custom.ifEmpty { p.title.ifEmpty { p.name } })
                    renamed.copy(name = uniqueName(st.profiles, renamed))
                }
            },
        )
    }

    /** Refreshes all subscriptions. Returns a message for the user. */
    suspend fun refreshAll(): String = coroutineScope {
        val ids = state.value.profiles.filter { it.kind == "subscription" }.map { it.id }
        if (ids.isEmpty()) fail(R.string.refresh_none)
        val failed = ids.map { id -> async { runCatching { refresh(id) }.isFailure } }.awaitAll().count { it }
        when {
            failed == ids.size -> fail(R.string.refresh_all_failed)
            failed > 0 -> Lang.str(R.string.refresh_some, ids.size - failed, ids.size)
            else -> Lang.str(R.string.refresh_all_done)
        }
    }

    suspend fun refresh(profileId: String) = withContext(Dispatchers.IO) {
        val p = _state.value.profiles.firstOrNull { it.id == profileId && it.kind == "subscription" }
            ?: fail(R.string.refresh_not_found)
        val sub = JSONObject(Velacore.fetchSubscription(p.url))
        val nodes = sub.optJSONArray("nodes").toNodes()
        var reload = false
        update { st ->
            // deleted or renamed meanwhile: use what is there now
            val cur = st.profiles.firstOrNull { it.id == p.id } ?: return@update st
            val next = st.copy(
                profiles = st.profiles.map {
                    if (it.id == cur.id) {
                        val fresh = subscriptionProfile(cur.id, cur.url, sub, nodes, cur.customName)
                        fresh.copy(name = uniqueName(st.profiles, fresh))
                    } else {
                        it
                    }
                },
            )
            withNewServers(st, next, cur.id).also { reload = it.second }.first
        }
        // a background refresh while connected: the running VPN gets the new servers
        if (reload) VpnManager.restart(appContext)
        Reminders.onSubscriptionsChanged(appContext)
    }

    /** Refreshes subscriptions whose update interval has passed. */
    suspend fun refreshDue() {
        val prefs = _state.value.sub
        if (prefs.noAutoUpdate) return
        val now = System.currentTimeMillis() / 1000
        for (p in _state.value.profiles.filter { it.kind == "subscription" }) {
            val interval = when {
                prefs.updateHours > 0 -> prefs.updateHours
                p.intervalHours > 0 -> p.intervalHours
                else -> 12
            }
            if (now - p.updatedAt > interval * 3600L) runCatching { refresh(p.id) }
        }
    }

    private fun subscriptionProfile(id: String, url: String, sub: JSONObject, nodes: List<Node>, customName: String = "") = Profile(
        id = id, name = customName.ifBlank { sub.optString("title").ifBlank { Uri.parse(url).host ?: url } },
        kind = "subscription", url = url, title = sub.optString("title"), customName = customName,
        nodes = nodes, upload = sub.optLong("upload"), download = sub.optLong("download"),
        total = sub.optLong("total"), expire = sub.optLong("expire"),
        intervalHours = sub.optInt("updateIntervalHours"),
        supportUrl = sub.optString("supportUrl").ifBlank { sub.optString("webPageUrl") },
        updatedAt = sub.optLong("updatedAt"),
        announce = sub.optString("announce").trim(),
    )

    fun deleteProfile(id: String) {
        update { st ->
            val rest = st.profiles.filterNot { it.id == id }
            if (st.activeProfile == id) {
                st.copy(profiles = rest, activeProfile = rest.firstOrNull()?.id ?: "", selectedNode = "auto")
            } else {
                st.copy(profiles = rest)
            }
        }
        Reminders.onSubscriptionsChanged(appContext)
    }

    fun deleteNode(profileId: String, nodeId: String) = update { st ->
        st.copy(
            profiles = st.profiles.map { p -> if (p.id == profileId) p.copy(nodes = p.nodes.filterNot { it.id == nodeId }) else p },
            selectedNode = if (st.selectedNode == nodeId) "auto" else st.selectedNode,
        )
    }

    fun select(profileId: String, nodeId: String) = update { it.copy(activeProfile = profileId, selectedNode = nodeId) }

    fun setSettings(settings: Settings) = update { it.copy(settings = settings) }

    fun setFavorite(nodeId: String, on: Boolean) = update {
        it.copy(favorites = if (on) it.favorites + nodeId else it.favorites - nodeId)
    }

    fun setTheme(theme: String) = update { it.copy(theme = theme) }

    // ------------------------------------------------------------ backup

    /** Everything needed to set Vela up on another device, as text for a QR code. */
    fun exportBackup(): String {
        val st = _state.value
        val subs = JSONArray()
        st.profiles.filter { it.kind == "subscription" }.forEach {
            subs.put(JSONObject().put("url", it.url).put("name", it.customName))
        }
        val links = JSONArray()
        st.profiles.firstOrNull { it.id == MANUAL_ID }?.nodes?.forEach { n ->
            n.raw.optString("link").takeIf { it.isNotBlank() }?.let { links.put(it) }
        }
        val favs = JSONArray()
        st.profiles.flatMap { it.nodes }.filter { it.id in st.favorites }.forEach { n ->
            n.raw.optString("link").takeIf { it.isNotBlank() }?.let { favs.put(it) }
        }
        val b = JSONObject()
            .put("subscriptions", subs).put("links", links).put("favorites", favs)
            .put("settings", st.settings.toJson())
            .put("extra", JSONObject().put("theme", st.theme).put("platform", "android"))
        return Velacore.encodeBackup(b.toString())
    }

    private suspend fun restore(b: JSONObject): String {
        val lines = mutableListOf<String>()
        val subs = b.optJSONArray("subscriptions")
        if (subs != null) for (i in 0 until subs.length()) subs.optJSONObject(i)?.optString("url")?.let { if (it.isNotBlank()) lines += it }
        lines += b.optJSONArray("links").strings()
        val msg = if (lines.isNotEmpty()) import(lines.joinToString("\n")) else ""
        if (subs != null) {
            for (i in 0 until subs.length()) {
                val o = subs.optJSONObject(i) ?: continue
                val name = o.optString("name")
                if (name.isBlank()) continue
                _state.value.profiles.firstOrNull { it.kind == "subscription" && it.url == o.optString("url") }?.let { rename(it.id, name) }
            }
        }
        val favLinks = b.optJSONArray("favorites").strings().toSet()
        val extra = b.optJSONObject("extra")
        update { st ->
            var settings = st.settings
            b.optJSONObject("settings")?.let { o ->
                val incoming = Settings.from(o)
                // app lists only make sense on the same kind of device
                settings = if (extra?.optString("platform") == "android") incoming
                else incoming.copy(appMode = st.settings.appMode, apps = st.settings.apps)
            }
            val favIds = st.profiles.flatMap { it.nodes }.filter { it.raw.optString("link") in favLinks }.map { it.id }
            st.copy(
                settings = settings,
                favorites = st.favorites + favIds,
                theme = extra?.optString("theme")?.takeIf { it in setOf("system", "light", "dark") } ?: st.theme,
            )
        }
        return if (msg.isEmpty()) Lang.str(R.string.backup_restored_settings) else Lang.str(R.string.backup_restored, msg)
    }

    fun setWasConnected(value: Boolean) = update { it.copy(wasConnected = value) }

    /** Builds the sing-box configuration for the active profile. */
    fun buildConfig(controllerPort: Int, controllerSecret: String): String {
        val st = _state.value
        val profile = st.active ?: fail(R.string.error_add_server_first)
        if (profile.nodes.isEmpty()) fail(R.string.error_no_servers)
        val req = JSONObject()
            .put("platform", "android")
            .put("nodes", JSONArray().apply { profile.nodes.forEach { put(it.raw) } })
            .put("selected", if (profile.id == st.activeProfile) st.selectedNode else "auto")
            .put("settings", st.settings.toJson())
            .put("dataDir", dataDir.absolutePath)
            .put("controllerPort", controllerPort)
            .put("controllerSecret", controllerSecret)
            .put("logPath", logFile.absolutePath)
        // automatic selection: favorites first, then the fastest, never the dead ones
        val ids = profile.nodes.map { it.id }.toSet()
        val p = pings.value
        req.put("favorites", JSONArray(st.favorites.filter { it in ids }))
        req.put("ranked", JSONArray(p.filter { it.key in ids && it.value > 0 }.entries.sortedBy { it.value }.map { it.key }))
        req.put("failed", JSONArray(p.filter { it.key in ids && it.value < 0 }.keys.toList()))
        return Velacore.buildConfig(req.toString())
    }

    fun randomId(bytes: Int = 8): String {
        val b = ByteArray(bytes)
        SecureRandom().nextBytes(b)
        return b.joinToString("") { "%02x".format(it) }
    }
}
