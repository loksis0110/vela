package app.vela.client.widget

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.widget.RemoteViews
import app.vela.client.Lang
import app.vela.client.R
import app.vela.client.data.Store
import app.vela.client.ui.MainActivity
import app.vela.client.ui.splitFlag
import app.vela.client.vpn.ConnState
import app.vela.client.vpn.VelaVpnService
import app.vela.client.vpn.VpnManager

/** Home screen widget: status, current server and a connect button. */
class VelaWidget : AppWidgetProvider() {
    override fun onUpdate(context: Context, manager: AppWidgetManager, ids: IntArray) {
        render(context, manager, ids)
    }

    companion object {
        /** Redraws all Vela widgets (called when the connection changes). */
        fun updateAll(context: Context) {
            val manager = AppWidgetManager.getInstance(context) ?: return
            val ids = runCatching { manager.getAppWidgetIds(ComponentName(context, VelaWidget::class.java)) }.getOrNull() ?: return
            if (ids.isNotEmpty()) render(context, manager, ids)
        }

        private fun render(base: Context, manager: AppWidgetManager, ids: IntArray) {
            val context = Lang.localized(base)
            val status = VpnManager.status.value
            val st = Store.state.value
            val profile = st.active
            val on = status.state == ConnState.Connected
            val busy = status.state == ConnState.Connecting || status.state == ConnState.Disconnecting
            val server = when {
                profile == null -> context.getString(R.string.widget_add_server)
                st.selectedNode == "auto" || profile.nodes.none { it.id == st.selectedNode } -> {
                    val live = profile.nodes.firstOrNull { it.id == status.activeNode }
                    if (on && live != null) context.getString(R.string.widget_auto_live, live.name)
                    else context.getString(R.string.widget_auto_profile, profile.name)
                }
                else -> profile.nodes.first { it.id == st.selectedNode }.let { (splitFlag(it.name).first?.let { f -> "$f " } ?: "") + splitFlag(it.name).second }
            }
            val flags = PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
            val open = PendingIntent.getActivity(context, 20, Intent(context, MainActivity::class.java), flags)
            val toggle: PendingIntent = when {
                on || busy -> PendingIntent.getService(
                    context, 21, Intent(context, VelaVpnService::class.java).setAction(VelaVpnService.ACTION_STOP), flags,
                )
                profile == null || VpnService.prepare(context) != null -> PendingIntent.getActivity(
                    context, 22,
                    Intent(context, MainActivity::class.java).setAction(MainActivity.ACTION_CONNECT).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                    flags,
                )
                else -> PendingIntent.getBroadcast(
                    context, 23,
                    Intent(context, WidgetActionReceiver::class.java).setAction(WidgetActionReceiver.ACTION_CONNECT),
                    flags,
                )
            }
            val views = RemoteViews(context.packageName, R.layout.widget_vela).apply {
                setTextViewText(
                    R.id.widget_status,
                    context.getString(
                        when (status.state) {
                            ConnState.Connected -> R.string.widget_on
                            ConnState.Connecting -> R.string.widget_connecting
                            else -> R.string.widget_off
                        },
                    ),
                )
                setTextViewText(R.id.widget_server, server)
                setImageViewResource(R.id.widget_dot, if (on) R.drawable.widget_dot_on else R.drawable.widget_dot_off)
                setTextViewText(R.id.widget_toggle, context.getString(if (on || busy) R.string.widget_disconnect else R.string.widget_connect))
                setInt(R.id.widget_toggle, "setBackgroundResource", if (on) R.drawable.widget_btn_on else R.drawable.widget_btn_off)
                setTextColor(R.id.widget_toggle, if (on) 0xFF06221B.toInt() else 0xFF1B1608.toInt())
                setOnClickPendingIntent(R.id.widget_toggle, toggle)
                setOnClickPendingIntent(R.id.widget_root, open)
            }
            ids.forEach { manager.updateAppWidget(it, views) }
        }
    }
}
