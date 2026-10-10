package app.vela.client

import android.app.Activity
import android.app.LocaleManager
import android.content.Context
import android.content.res.Configuration
import android.content.res.Resources
import android.os.Build
import android.os.LocaleList
import app.vela.velacore.Velacore
import java.util.Locale

/**
 * App language: follows the system by default, or Russian / English chosen in
 * Settings. Android 13+ keeps the choice itself (per-app language, also shown
 * in the system settings); older versions keep it here and wrap contexts.
 */
object Lang {
    private const val PREFS = "lang"
    private const val KEY = "lang"
    private var app: Context? = null

    @Volatile private var cached: Context? = null

    fun init(context: Context) {
        app = context.applicationContext ?: context
        cached = null
    }

    private fun appContext(fallback: Context? = null): Context =
        app ?: (fallback?.applicationContext ?: fallback)?.also { init(it) } ?: error("Lang is not initialized")

    /** "system", "ru" or "en". */
    fun choice(context: Context? = null): String {
        val ctx = appContext(context)
        if (Build.VERSION.SDK_INT >= 33) {
            val list = runCatching { ctx.getSystemService(LocaleManager::class.java)?.applicationLocales }.getOrNull()
            if (list == null || list.isEmpty) return "system"
            return when (list[0].language) {
                "ru" -> "ru"
                "en" -> "en"
                else -> "system"
            }
        }
        return ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY, "system") ?: "system"
    }

    /** Switches the language; the activity is recreated with the new texts. */
    fun set(activity: Activity, lang: String) {
        if (lang == choice(activity)) return
        if (Build.VERSION.SDK_INT >= 33) {
            val lm = activity.getSystemService(LocaleManager::class.java)
            lm.applicationLocales = if (lang == "system") LocaleList.getEmptyLocaleList() else LocaleList.forLanguageTags(lang)
            cached = null
            applyToCore()
            // Android recreates the activity itself
        } else {
            activity.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY, lang).apply()
            cached = null
            applyToCore()
            activity.recreate()
        }
    }

    private fun locales(context: Context?): LocaleList = when (val c = choice(context)) {
        "system" -> Resources.getSystem().configuration.locales
        else -> LocaleList.forLanguageTags(c)
    }

    /** A context whose resources use the app language. */
    fun localized(base: Context): Context {
        val conf = Configuration(base.resources.configuration)
        conf.setLocales(locales(base))
        return base.createConfigurationContext(conf)
    }

    /** For attachBaseContext of activities and services (Android 12 and older). */
    fun wrap(base: Context): Context {
        if (Build.VERSION.SDK_INT >= 33) return base
        return if (choice(base) == "system") base else localized(base)
    }

    /** Localized resources for code outside of activities (store, service, widget). */
    val res: Context
        get() = cached ?: localized(appContext()).also { cached = it }

    fun str(id: Int, vararg args: Any): String = if (args.isEmpty()) res.getString(id) else res.getString(id, *args)

    /** "ru" or "en": the language the texts are actually shown in. */
    fun effective(): String = res.getString(R.string.lang_code)

    /** Locale for dates and numbers, matching the texts. */
    fun locale(): Locale = Locale.forLanguageTag(effective())

    /** The system language changed. */
    fun onConfigurationChanged() {
        cached = null
        applyToCore()
    }

    /** Messages from the Go core in the same language. */
    fun applyToCore() {
        runCatching { Velacore.setLanguage(effective()) }
    }
}
