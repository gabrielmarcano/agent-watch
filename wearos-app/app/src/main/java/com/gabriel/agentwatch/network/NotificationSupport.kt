package com.gabriel.agentwatch.network

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.util.Log
import com.gabriel.agentwatch.MainActivity

/** The app's notification channels. Idempotent: creating an existing channel is a no-op. */
object NotificationChannels {
    const val BLOCKED = "agent_blocked"
    const val DONE = "agent_done"
    /** Low importance: action feedback, success or failure, never re-alerts. */
    const val FEEDBACK = "agent_watch_feedback"

    fun ensure(context: Context) {
        val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        manager.createNotificationChannel(
            NotificationChannel(BLOCKED, "Agent Blocked (Approvals)", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "Urgent alerts when an agent is waiting for your approval"
                enableLights(true)
                enableVibration(true)
            }
        )
        manager.createNotificationChannel(
            NotificationChannel(DONE, "Agent Completed", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "Notifications when an agent task finishes"
            }
        )
        manager.createNotificationChannel(
            NotificationChannel(FEEDBACK, "Agent Action Feedback", NotificationManager.IMPORTANCE_LOW).apply {
                description = "Brief silent feedback after acting on an agent notification"
            }
        )
    }
}

/** PendingIntents whose identity is unique per (pane, action); see [AgentNotifications.intentUri]. */
object NotificationIntents {
    /** Opens the app on [paneId] (or on the list for a digest). */
    fun openApp(context: Context, paneId: String?): PendingIntent {
        val intent = Intent(context, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK
            if (!paneId.isNullOrBlank()) {
                putExtra("pane_id", paneId)
                data = Uri.parse(AgentNotifications.intentUri(paneId, NotificationAction.OPEN))
            }
        }
        val requestCode = if (paneId.isNullOrBlank()) {
            AgentNotifications.DIGEST_ID
        } else {
            AgentNotifications.requestCode(paneId, NotificationAction.OPEN)
        }
        return PendingIntent.getActivity(
            context, requestCode, intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
    }

    /** A broadcast to [NotificationActionReceiver] carrying `pane_id` plus whatever [extras] adds. */
    fun receiverAction(
        context: Context,
        paneId: String,
        action: NotificationAction,
        broadcastAction: String,
        mutable: Boolean = false,
        extras: Intent.() -> Unit
    ): PendingIntent {
        val intent = Intent(context, NotificationActionReceiver::class.java).apply {
            this.action = broadcastAction
            data = Uri.parse(AgentNotifications.intentUri(paneId, action))
            putExtra("pane_id", paneId)
            extras()
        }
        val mutability = if (mutable) PendingIntent.FLAG_MUTABLE else PendingIntent.FLAG_IMMUTABLE
        return PendingIntent.getBroadcast(
            context, AgentNotifications.requestCode(paneId, action), intent,
            PendingIntent.FLAG_UPDATE_CURRENT or mutability
        )
    }

    /** Extras that let [ApprovalNotifications] find this notification again. */
    fun tag(kind: String, paneId: String? = null, seq: Long = 0L): Bundle = Bundle().apply {
        putString(AgentNotifications.EXTRA_KIND, kind)
        if (paneId != null) putString(AgentNotifications.EXTRA_PANE, paneId)
        putLong(AgentNotifications.EXTRA_SEQ, seq)
    }
}

/** Dismisses approval notifications that no longer match an agent's state. */
object ApprovalNotifications {
    private const val TAG = "ApprovalNotifications"

    private fun manager(context: Context) =
        context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager

    private fun shown(manager: NotificationManager): List<ShownApproval> =
        manager.activeNotifications.mapNotNull { sbn ->
            val extras = sbn.notification.extras
            if (extras.getString(AgentNotifications.EXTRA_KIND) != AgentNotifications.KIND_APPROVAL) return@mapNotNull null
            val paneId = extras.getString(AgentNotifications.EXTRA_PANE) ?: return@mapNotNull null
            ShownApproval(sbn.id, paneId, extras.getLong(AgentNotifications.EXTRA_SEQ, 0L))
        }

    /** A data-only `resolved` push: dismiss the pane's approval, show nothing. */
    fun onResolved(context: Context, message: PushMessage.Resolved) {
        try {
            val manager = manager(context)
            val approval = shown(manager).firstOrNull { it.paneId == message.paneId }
            if (shouldDismissOnResolved(approval, message.seq)) manager.cancel(approval!!.notificationId)
        } catch (e: RuntimeException) {
            Log.w(TAG, "Could not dismiss a resolved approval: ${e.javaClass.simpleName}")
        }
    }

    /** SSE or refresh news from the repository. */
    fun onAgentsUpdated(context: Context, update: AgentsUpdate) {
        if (update is AgentsUpdate.Changed && update.agent.status == "blocked") return // nothing to dismiss
        try {
            val manager = manager(context)
            approvalsToDismiss(shown(manager), update).forEach(manager::cancel)
        } catch (e: RuntimeException) {
            Log.w(TAG, "Could not dismiss stale approvals: ${e.javaClass.simpleName}")
        }
    }
}
