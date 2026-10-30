package com.gabriel.agentwatch.network

import android.app.NotificationManager
import android.content.Context
import android.net.Uri
import android.util.Log
import com.gabriel.agentwatch.model.AgentKey

object NotificationConstants {
    /** Fixed notification id for digest pushes (they have no pane). */
    const val DIGEST_ID = 9999

    /** Notification extras that tag what a shown notification is (read back via activeNotifications). */
    const val EXTRA_KIND = "aw_kind"
    const val EXTRA_PANE = "aw_pane"
    const val EXTRA_HOST = "aw_host"
    const val EXTRA_SEQ = "aw_seq"
    const val EXTRA_LABEL = "aw_label"

    const val KIND_APPROVAL = "approval"
    const val KIND_DONE = "done"
    const val KIND_FEEDBACK = "feedback"
}

/** One notification id per agent, (host, pane). Without a host the id is the pane's token hash. */
fun notificationIdFor(agent: AgentKey): Int = agent.token.hashCode() and 0x7FFFFFFF

/**
 * Unique data URI for notification PendingIntents.
 * Scheme defaults to "agentwatch" for watch and can be set to "agentwatch-mobile" for mobile.
 */
fun notificationIntentUri(
    agent: AgentKey,
    actionPath: String,
    optionId: String? = null,
    scheme: String = "agentwatch"
): String {
    val path = if (optionId.isNullOrBlank()) actionPath else "$actionPath/${Uri.encode(optionId)}"
    val base = "$scheme://notification/$path/${Uri.encode(agent.paneId)}"
    return if (agent.host.isEmpty()) base else "$base?host=${Uri.encode(agent.host)}"
}

/** Distinct request code per action for an agent. */
fun notificationRequestCode(agent: AgentKey, actionOrdinal: Int, multiplier: Int = 4): Int =
    notificationIdFor(agent) * multiplier + actionOrdinal

/** Scans active notifications for currently shown approval notifications. */
fun findShownApprovals(manager: NotificationManager): List<ShownApproval> {
    return try {
        manager.activeNotifications.mapNotNull { sbn ->
            val extras = sbn.notification.extras
            if (extras.getString(NotificationConstants.EXTRA_KIND) != NotificationConstants.KIND_APPROVAL) return@mapNotNull null
            val paneId = extras.getString(NotificationConstants.EXTRA_PANE) ?: return@mapNotNull null
            val host = extras.getString(NotificationConstants.EXTRA_HOST).orEmpty()
            val seq = extras.getLong(NotificationConstants.EXTRA_SEQ, 0L)
            ShownApproval(sbn.id, paneId, seq, host)
        }
    } catch (e: RuntimeException) {
        emptyList()
    }
}

/**
 * Dismisses an active approval notification when a [PushMessage.Resolved] push arrives,
 * unless the shown prompt is newer than the resolved sequence.
 */
fun dismissResolved(context: Context, message: PushMessage.Resolved): Boolean {
    return try {
        val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as? NotificationManager ?: return false
        val shown = findShownApprovals(manager)
        val approval = shown.firstOrNull { it.key == message.key }
        if (shouldDismissOnResolved(approval, message.seq)) {
            manager.cancel(approval!!.notificationId)
            true
        } else false
    } catch (e: RuntimeException) {
        Log.w("NotificationDismissal", "Could not dismiss a resolved approval: ${e.javaClass.simpleName}")
        false
    }
}

/**
 * Dismisses approval notifications made obsolete by an [AgentsUpdate].
 */
fun dismissStaleApprovals(context: Context, update: AgentsUpdate): List<Int> {
    if (update is AgentsUpdate.Changed && update.agent.status == "blocked") return emptyList()
    return try {
        val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as? NotificationManager ?: return emptyList()
        val toDismiss = approvalsToDismiss(findShownApprovals(manager), update)
        toDismiss.forEach(manager::cancel)
        toDismiss
    } catch (e: RuntimeException) {
        Log.w("NotificationDismissal", "Could not dismiss stale approvals: ${e.javaClass.simpleName}")
        emptyList()
    }
}

/** Cancels all active notifications posted by this application. */
fun cancelAllNotifications(context: Context) {
    try {
        val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as? NotificationManager
        manager?.cancelAll()
    } catch (e: RuntimeException) {
        Log.w("NotificationDismissal", "Could not cancel all notifications: ${e.javaClass.simpleName}")
    }
}
