package com.gabriel.agentwatch.network

import android.app.Notification
import android.app.NotificationManager
import android.content.Context
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.RemoteInput
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.util.MarkdownFormatter
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage

class MyFirebaseMessagingService : FirebaseMessagingService() {

    companion object {
        private const val TAG = "FCM"
    }

    override fun onNewToken(token: String) {
        super.onNewToken(token)
        Log.d(TAG, "New FCM token received (${token.length} chars)")
        PushRegistration.onToken(this, token)
    }

    override fun onMessageReceived(remoteMessage: RemoteMessage) {
        super.onMessageReceived(remoteMessage)
        val message = PushMessage.parse(remoteMessage.data)
        Log.d(TAG, "Push: ${message.logSummary()}")

        val notifManager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        when (message) {
            is PushMessage.Resolved -> {
                message.seq?.let { seq ->
                    val prefs = Prefs(this)
                    prefs.resolvedSeqs = prefs.resolvedSeqs.record(message.paneId, seq)
                }
                ApprovalNotifications.onResolved(this, message)
            }
            is PushMessage.Blocked -> {
                if (!shouldShowBlocked(message.seq, Prefs(this).resolvedSeqs.lastFor(message.paneId))) {
                    Log.d(TAG, "Dropping a stale blocked push: pane=${message.paneId} seq=${message.seq} already resolved")
                    return
                }
                NotificationChannels.ensure(this)
                notifManager.notify(AgentNotifications.idForPane(message.paneId), blockedNotification(message))
            }
            is PushMessage.Done -> {
                NotificationChannels.ensure(this)
                notifManager.notify(AgentNotifications.idForPane(message.paneId), doneNotification(message))
            }
            is PushMessage.Digest -> {
                NotificationChannels.ensure(this)
                notifManager.notify(AgentNotifications.DIGEST_ID, digestNotification(message))
            }
            is PushMessage.Ignored -> return
        }
        // Agent state changed while the app may be closed: refresh the complication and tile (throttled).
        SurfaceUpdates.request(this)
    }

    private fun blockedNotification(message: PushMessage.Blocked): Notification {
        val paneId = message.paneId
        val notifId = AgentNotifications.idForPane(paneId)
        val openPendingIntent = NotificationIntents.openApp(this, paneId)

        val builder = NotificationCompat.Builder(this, NotificationChannels.BLOCKED)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(message.title)
            .setContentText(MarkdownFormatter.clean(message.body))
            .setAutoCancel(true)
            .setContentIntent(openPendingIntent)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .addExtras(NotificationIntents.tag(AgentNotifications.KIND_APPROVAL, paneId, message.seq))

        // Allow Action
        if (message.allowOptionId.isNotBlank()) {
            val allowPending = NotificationIntents.receiverAction(
                this, paneId, NotificationAction.ALLOW, NotificationActionReceiver.ACTION_ANSWER
            ) {
                putExtra("option_id", message.allowOptionId)
                putExtra("state_change_seq", message.seq)
                putExtra("fingerprint", message.fingerprint)
                putExtra("notif_id", notifId)
            }
            builder.addAction(android.R.drawable.checkbox_on_background, "Allow", allowPending)
        }

        // Deny Action: the deny option when the prompt has one, else cancel (contracts §4.1)
        val hasDeny = message.denyOptionId.isNotBlank()
        val denyPending = NotificationIntents.receiverAction(
            this, paneId, NotificationAction.DENY,
            if (hasDeny) NotificationActionReceiver.ACTION_ANSWER else NotificationActionReceiver.ACTION_CANCEL
        ) {
            if (hasDeny) {
                putExtra("option_id", message.denyOptionId)
                putExtra("is_deny", true)
            }
            putExtra("fingerprint", message.fingerprint)
            putExtra("state_change_seq", message.seq)
            putExtra("notif_id", notifId)
        }
        builder.addAction(android.R.drawable.ic_delete, if (hasDeny) "Deny" else "Cancel", denyPending)

        // Open Action
        builder.addAction(android.R.drawable.ic_menu_view, "Open", openPendingIntent)
        return builder.build()
    }

    private fun doneNotification(message: PushMessage.Done): Notification {
        val paneId = message.paneId
        val notifId = AgentNotifications.idForPane(paneId)

        // Done Action: Reply via RemoteInput
        val remoteInput = RemoteInput.Builder("KEY_TEXT_REPLY")
            .setLabel("Reply to ${message.label}...")
            .build()
        val replyPending = NotificationIntents.receiverAction(
            this, paneId, NotificationAction.REPLY, NotificationActionReceiver.ACTION_PROMPT, mutable = true
        ) {
            putExtra("state_change_seq", message.seq)
            putExtra("notif_id", notifId)
        }
        val replyAction = NotificationCompat.Action.Builder(
            android.R.drawable.ic_btn_speak_now,
            "Reply",
            replyPending
        ).addRemoteInput(remoteInput).build()

        return NotificationCompat.Builder(this, NotificationChannels.DONE)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(message.title)
            .setContentText(MarkdownFormatter.clean(message.body))
            .setAutoCancel(true)
            .setContentIntent(NotificationIntents.openApp(this, paneId))
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .addExtras(NotificationIntents.tag(AgentNotifications.KIND_DONE, paneId, message.seq))
            .addAction(replyAction)
            .build()
    }

    private fun digestNotification(message: PushMessage.Digest): Notification =
        NotificationCompat.Builder(this, NotificationChannels.DONE)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(message.title)
            .setContentText(MarkdownFormatter.clean(message.body))
            .setAutoCancel(true)
            .setContentIntent(NotificationIntents.openApp(this, null))
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .build()
}
