package app.vela.client.notify

import android.annotation.SuppressLint
import android.app.AlarmManager
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build
import androidx.core.app.NotificationCompat
import app.vela.client.Lang
import app.vela.client.R
import app.vela.client.data.Profile
import app.vela.client.data.Store
import app.vela.client.ui.MainActivity

/**
 * Reminders about subscriptions: 3 days and 1 day before the end, when it has
 * ended, and when 90% of the traffic is used. Each alert is shown once per
 * subscription and period (a renewed subscription has a new end date).
 */
object Reminders {
    const val CHANNEL = "subscription"
    private const val ACTION_ALARM = "app.vela.client.REMINDER"
    private const val DAY = 86_400L
    private const val SHOWN = "alertsShown"

    /** Creates (or renames, after a language change) the notification channel. */
    fun ensureChannel(context: Context) {
        if (Build.VERSION.SDK_INT < 26) return
        val nm = context.getSystemService(NotificationManager::class.java) ?: return
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL, Lang.str(R.string.channel_subscription), NotificationManager.IMPORTANCE_DEFAULT),
        )
    }

    /** After a subscription was added, refreshed or deleted. */
    fun onSubscriptionsChanged(context: Context) {
        runCatching {
            check(context, traffic = true)
            schedule(context)
        }
    }

    /** Reminders were switched on or off. */
    fun onChanged(context: Context) {
        runCatching {
            if (Store.state.value.noReminders) cancel(context) else onSubscriptionsChanged(context)
        }
    }

    /** At app start, on the alarm and after a reboot. */
    fun onWake(context: Context) {
        runCatching {
            check(context, traffic = false)
            schedule(context)
        }
    }

    private fun subscriptions(): List<Profile> = Store.state.value.profiles.filter { it.kind == "subscription" }

    private fun shown(): MutableSet<String> = Store.prefs.getStringSet(SHOWN, emptySet())!!.toMutableSet()

    private fun saveShown(set: Set<String>) {
        Store.prefs.edit().putStringSet(SHOWN, set).apply()
    }

    /** Shows the alerts that are due now. */
    private fun check(context: Context, traffic: Boolean) {
        if (Store.state.value.noReminders) return
        val nm = context.getSystemService(NotificationManager::class.java) ?: return
        val canNotify = Build.VERSION.SDK_INT < 24 || nm.areNotificationsEnabled()
        val now = System.currentTimeMillis() / 1000
        val shown = shown()
        val before = shown.toSet()
        val subs = subscriptions()
        // forget alerts of deleted subscriptions
        shown.removeAll { key -> subs.none { key.startsWith(it.id + ":") } }
        for (p in subs) {
            if (p.expire > 0) {
                val left = p.expire - now
                val due = when {
                    left <= 0 -> "e0"
                    left <= DAY -> "e1"
                    left <= 3 * DAY -> "e3"
                    else -> null
                }
                if (due != null) {
                    val key = "${p.id}:$due:${p.expire}"
                    if (key !in shown && canNotify) {
                        notify(context, nm, p, due)
                        shown += key
                        // a later alert makes the earlier ones pointless
                        if (due == "e0") { shown += "${p.id}:e1:${p.expire}"; shown += "${p.id}:e3:${p.expire}" }
                        if (due == "e1") shown += "${p.id}:e3:${p.expire}"
                    }
                }
            }
            if (p.total > 0) {
                val key = "${p.id}:t90:${p.total}"
                val used = p.upload + p.download
                if (used * 10 >= p.total * 9) {
                    if (traffic && key !in shown && canNotify) {
                        notify(context, nm, p, "t90")
                        shown += key
                    }
                } else {
                    // the traffic was reset (new period): alert again next time
                    shown.removeAll { it.startsWith("${p.id}:t90:") }
                }
            }
        }
        if (shown != before) saveShown(shown)
    }

    /** Wakes up at the next alert time (inexact, no special permission). */
    private fun schedule(context: Context) {
        val am = context.getSystemService(AlarmManager::class.java) ?: return
        if (Store.state.value.noReminders) {
            cancel(context)
            return
        }
        val now = System.currentTimeMillis() / 1000
        val shown = shown()
        var next = Long.MAX_VALUE
        for (p in subscriptions()) {
            if (p.expire <= 0) continue
            listOf("e3" to p.expire - 3 * DAY, "e1" to p.expire - DAY, "e0" to p.expire).forEach { (kind, at) ->
                if (at > now && "${p.id}:$kind:${p.expire}" !in shown && at < next) next = at
            }
        }
        val pi = alarmIntent(context)
        if (next == Long.MAX_VALUE) {
            am.cancel(pi)
            return
        }
        // a minute late is fine: the subscription has surely changed state by then
        val at = (next + 60) * 1000
        if (Build.VERSION.SDK_INT >= 23) am.setAndAllowWhileIdle(AlarmManager.RTC_WAKEUP, at, pi)
        else am.set(AlarmManager.RTC_WAKEUP, at, pi)
    }

    private fun cancel(context: Context) {
        context.getSystemService(AlarmManager::class.java)?.cancel(alarmIntent(context))
    }

    private fun alarmIntent(context: Context): PendingIntent = PendingIntent.getBroadcast(
        context, 40,
        Intent(context, ReminderReceiver::class.java).setAction(ACTION_ALARM),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
    )

    @SuppressLint("MissingPermission")
    private fun notify(context: Context, nm: NotificationManager, p: Profile, kind: String) {
        ensureChannel(context)
        val (title, text) = when (kind) {
            "e3" -> Lang.str(R.string.remind_3days_title, p.name) to Lang.str(R.string.remind_expire_text)
            "e1" -> Lang.str(R.string.remind_1day_title, p.name) to Lang.str(R.string.remind_expire_text)
            "e0" -> Lang.str(R.string.remind_ended_title, p.name) to Lang.str(R.string.remind_ended_text)
            else -> Lang.str(R.string.remind_traffic_title, p.name) to Lang.str(R.string.remind_traffic_text)
        }
        val open = PendingIntent.getActivity(
            context, 41, Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val builder = NotificationCompat.Builder(context, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_vela)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setContentIntent(open)
            .setAutoCancel(true)
            .setCategory(NotificationCompat.CATEGORY_REMINDER)
        val support = p.supportUrl
        if (support.startsWith("https://") || support.startsWith("http://") || support.startsWith("tg://")) {
            val renew = PendingIntent.getActivity(
                context, 42 + (p.id.hashCode() and 0xffff),
                Intent(Intent.ACTION_VIEW, android.net.Uri.parse(support)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
            builder.addAction(0, Lang.str(R.string.remind_renew), renew)
        }
        // one notification per subscription and kind of alert (expiry / traffic)
        val id = 1000 + ((p.id + (if (kind == "t90") ":t" else ":e")).hashCode() and 0x7fff)
        runCatching { nm.notify(id, builder.build()) }
    }
}

/** Alarm of [Reminders], and rescheduling after a reboot or an app update. */
class ReminderReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        when (intent.action) {
            Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED, "app.vela.client.REMINDER" ->
                Reminders.onWake(context)
        }
    }
}
