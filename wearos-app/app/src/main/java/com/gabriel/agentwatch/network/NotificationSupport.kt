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
import com.gabriel.agentwatch.model.AgentKey

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

/** PendingIntents whose identity is unique per (host, pane, action); see [AgentNotifications.intentUri]. */
object NotificationIntents {
    /** The intent extras that name an agent: its pane and its host ("" before hosts). */
    const val EXTRA_PANE_ID = "pane_id"
    const val EXTRA_HOST = "host"

    /** Opens the app on [agent] (or on the list for a digest). */
    fun openApp(context: Context, agent: AgentKey?): PendingIntent {
        val intent = Intent(context, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK
            if (agent != null && agent.paneId.isNotBlank()) {
                putExtra(EXTRA_PANE_ID, agent.paneId)
                putExtra(EXTRA_HOST, agent.host)
                data = Uri.parse(AgentNotifications.intentUri(agent, NotificationAction.OPEN))
            }
        }
        val requestCode = if (agent == null || agent.paneId.isBlank()) {
            AgentNotifications.DIGEST_ID
        } else {
            AgentNotifications.requestCode(agent, NotificationAction.OPEN)
        }
        return PendingIntent.getActivity(
            context, requestCode, intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
    }

    /** A broadcast to [NotificationActionReceiver] carrying the agent's pane and host plus whatever [extras] adds. */
    fun receiverAction(
        context: Context,
        agent: AgentKey,
        action: NotificationAction,
        broadcastAction: String,
        mutable: Boolean = false,
        optionId: String? = null,
        extras: Intent.() -> Unit
    ): PendingIntent {
        val intent = Intent(context, NotificationActionReceiver::class.java).apply {
            this.action = broadcastAction
            data = Uri.parse(AgentNotifications.intentUri(agent, action, optionId))
            putExtra(EXTRA_PANE_ID, agent.paneId)
            putExtra(EXTRA_HOST, agent.host)
            extras()
        }
        val mutability = if (mutable) PendingIntent.FLAG_MUTABLE else PendingIntent.FLAG_IMMUTABLE
        return PendingIntent.getBroadcast(
            context, AgentNotifications.requestCode(agent, action), intent,
            PendingIntent.FLAG_UPDATE_CURRENT or mutability
        )
    }

    /** Extras that let [ApprovalNotifications] find this notification again. */
    fun tag(kind: String, agent: AgentKey? = null, seq: Long = 0L): Bundle = Bundle().apply {
        putString(AgentNotifications.EXTRA_KIND, kind)
        if (agent != null) {
            putString(AgentNotifications.EXTRA_PANE, agent.paneId)
            putString(AgentNotifications.EXTRA_HOST, agent.host)
        }
        putLong(AgentNotifications.EXTRA_SEQ, seq)
    }

    /** The agent an intent names (its extras); null without a pane. A missing host is "" (an intent from before hosts). */
    fun agentOf(intent: Intent?): AgentKey? {
        val paneId = intent?.getStringExtra(EXTRA_PANE_ID)?.takeIf { it.isNotBlank() } ?: return null
        return AgentKey(intent.getStringExtra(EXTRA_HOST).orEmpty(), paneId)
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
            val host = extras.getString(AgentNotifications.EXTRA_HOST).orEmpty()
            ShownApproval(sbn.id, paneId, extras.getLong(AgentNotifications.EXTRA_SEQ, 0L), host)
        }

    /** A data-only `resolved` push: dismiss the agent's approval, show nothing. */
    fun onResolved(context: Context, message: PushMessage.Resolved) {
        try {
            val manager = manager(context)
            val approval = shown(manager).firstOrNull { it.key == message.key }
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
