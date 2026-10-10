package app.vela.client.widget

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import app.vela.client.Lang
import app.vela.client.R
import app.vela.client.ui.MainActivity
import app.vela.client.vpn.VelaVpnService

/**
 * «Подключить» on the widget. VPN permission is checked at the moment of the
 * tap (it may have gone to another VPN app since the widget was drawn).
 */
class WidgetActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != ACTION_CONNECT) return
        val start = Intent(context, VelaVpnService::class.java).setAction(VelaVpnService.ACTION_START)
        val ok = VpnService.prepare(context) == null && runCatching {
            ContextCompat.startForegroundService(context, start)
        }.onFailure { Log.w("VelaWidget", "start failed", it) }.isSuccess
        if (!ok) askToOpen(context)
        VelaWidget.updateAll(context)
    }

    /** The app has to be opened once: Android asks for VPN permission there. */
    private fun askToOpen(base: Context) {
        val context = Lang.localized(base)
        val nm = context.getSystemService(NotificationManager::class.java) ?: return
        if (Build.VERSION.SDK_INT >= 26 && nm.getNotificationChannel(CHANNEL) == null) {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, context.getString(R.string.widget_channel), NotificationManager.IMPORTANCE_DEFAULT))
        }
        val open = PendingIntent.getActivity(
            context, 30,
            Intent(context, MainActivity::class.java).setAction(MainActivity.ACTION_CONNECT).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val n = NotificationCompat.Builder(context, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_vela)
            .setContentTitle(context.getString(R.string.widget_need_app))
            .setContentText(context.getString(R.string.widget_need_app_text))
            .setContentIntent(open)
            .setAutoCancel(true)
            .build()
        runCatching { nm.notify(NOTIFICATION_ID, n) }
    }

    companion object {
        const val ACTION_CONNECT = "app.vela.client.WIDGET_CONNECT"
        private const val CHANNEL = "widget"
        private const val NOTIFICATION_ID = 7
    }
}
