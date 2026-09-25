package com.gabriel.agentwatch.network

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.RemoteInput
import com.gabriel.agentwatch.MainActivity
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.util.MarkdownFormatter
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

class MyFirebaseMessagingService : FirebaseMessagingService() {

    companion object {
        private const val TAG = "FCM"
        private const val CHANNEL_BLOCKED = "agent_blocked"
        private const val CHANNEL_DONE = "agent_done"
        private const val CHANNEL_FEEDBACK = "agent_watch_feedback"
    }

    override fun onNewToken(token: String) {
        super.onNewToken(token)
        Log.d(TAG, "New FCM token generated: $token")

        val prefs = Prefs(this)
        prefs.fcmToken = token

        if (prefs.isPaired && token != prefs.fcmRegisteredToken) {
            CoroutineScope(Dispatchers.IO).launch {
                val client = RelayClient(prefs.relayUrl, prefs.deviceToken)
                val res = client.registerPush(token)
                if (res.isSuccess) {
                    prefs.fcmRegisteredToken = token
                    Log.d(TAG, "Successfully registered FCM token with relay")
                } else {
                    Log.e(TAG, "Failed to register FCM token: ${res.exceptionOrNull()?.message}")
                }
            }
        }
    }

    override fun onMessageReceived(remoteMessage: RemoteMessage) {
        super.onMessageReceived(remoteMessage)
        Log.d(TAG, "Received FCM message: ${remoteMessage.data}")

        val data = remoteMessage.data
        if (data.isEmpty()) return

        val paneId = data["pane_id"] ?: ""
        val agent = data["agent"] ?: ""
        val label = data["label"] ?: agent
        val event = data["event"] ?: "blocked"
        val isBlocked = event == "blocked" || event == "agent_blocked"
        val isDone = event == "done" || event == "agent_done"
        val title = data["title"] ?: (if (event == "digest") "Agent Watch" else if (isDone) "$label finished" else "$label needs you")
        val body = data["body"] ?: ""
        val seqStr = data["state_change_seq"] ?: "0"
        val stateChangeSeq = seqStr.toLongOrNull() ?: 0L
        val fingerprint = data["fingerprint"] ?: ""
        val allowOptionId = data["allow_option_id"] ?: ""
        val denyOptionId = data["deny_option_id"] ?: ""

        createNotificationChannels()

        val notifId = if (event == "digest" || paneId.isBlank()) 9999 else (paneId.hashCode() and 0x7FFFFFFF)
        val cleanedBody = MarkdownFormatter.clean(body)

        val notifManager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager

        // Content intent: open MainActivity deep linked to this pane
        val openIntent = Intent(this, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK
            if (paneId.isNotBlank()) {
                putExtra("pane_id", paneId)
            }
        }
        val openPendingIntent = PendingIntent.getActivity(
            this,
            notifId,
            openIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val channelId = if (isBlocked) CHANNEL_BLOCKED else CHANNEL_DONE
        val priority = if (isBlocked) NotificationCompat.PRIORITY_HIGH else NotificationCompat.PRIORITY_DEFAULT

        val builder = NotificationCompat.Builder(this, channelId)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(title)
            .setContentText(cleanedBody)
            .setAutoCancel(true)
            .setContentIntent(openPendingIntent)
            .setPriority(priority)

        val baseRequestCode = (notifId % 100000) * 10

        if (isBlocked) {
            // Allow Action
            if (allowOptionId.isNotBlank()) {
                val allowIntent = Intent(this, NotificationActionReceiver::class.java).apply {
                    action = NotificationActionReceiver.ACTION_ANSWER
                    putExtra("pane_id", paneId)
                    putExtra("option_id", allowOptionId)
                    putExtra("state_change_seq", stateChangeSeq)
                    putExtra("fingerprint", fingerprint)
                    putExtra("notif_id", notifId)
                }
                val allowPending = PendingIntent.getBroadcast(
                    this,
                    baseRequestCode + 1,
                    allowIntent,
                    PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
                )
                builder.addAction(android.R.drawable.checkbox_on_background, "Allow", allowPending)
            }

            // Deny Action
            val denyIntent = Intent(this, NotificationActionReceiver::class.java).apply {
                if (denyOptionId.isNotBlank()) {
                    action = NotificationActionReceiver.ACTION_ANSWER
                    putExtra("option_id", denyOptionId)
                    putExtra("fingerprint", fingerprint)
                    putExtra("is_deny", true)
                } else {
                    action = NotificationActionReceiver.ACTION_CANCEL
                }
                putExtra("pane_id", paneId)
                putExtra("state_change_seq", stateChangeSeq)
                putExtra("notif_id", notifId)
            }
            val denyPending = PendingIntent.getBroadcast(
                this,
                baseRequestCode + 2,
                denyIntent,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
            )
            val denyLabel = if (denyOptionId.isNotBlank()) "Deny" else "Cancel"
            builder.addAction(android.R.drawable.ic_delete, denyLabel, denyPending)

            // Open Action
            builder.addAction(android.R.drawable.ic_menu_view, "Open", openPendingIntent)
        } else if (isDone) {
            // Done Action: Reply via RemoteInput
            val remoteInput = RemoteInput.Builder("KEY_TEXT_REPLY")
                .setLabel("Reply to $label...")
                .build()

            val replyIntent = Intent(this, NotificationActionReceiver::class.java).apply {
                action = NotificationActionReceiver.ACTION_PROMPT
                putExtra("pane_id", paneId)
                putExtra("state_change_seq", stateChangeSeq)
                putExtra("notif_id", notifId)
            }
            val replyPending = PendingIntent.getBroadcast(
                this,
                baseRequestCode + 3,
                replyIntent,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE
            )

            val replyAction = NotificationCompat.Action.Builder(
                android.R.drawable.ic_btn_speak_now,
                "Reply",
                replyPending
            ).addRemoteInput(remoteInput).build()

            builder.addAction(replyAction)
        }

        notifManager.notify(notifId, builder.build())
    }

    private fun createNotificationChannels() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val notifManager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager

            val blockedChannel = NotificationChannel(
                CHANNEL_BLOCKED,
                "Agent Blocked (Approvals)",
                NotificationManager.IMPORTANCE_HIGH
            ).apply {
                description = "Urgent alerts when an agent is waiting for your approval"
                enableLights(true)
                enableVibration(true)
            }

            val doneChannel = NotificationChannel(
                CHANNEL_DONE,
                "Agent Completed",
                NotificationManager.IMPORTANCE_DEFAULT
            ).apply {
                description = "Notifications when an agent task finishes"
            }

            val feedbackChannel = NotificationChannel(
                CHANNEL_FEEDBACK,
                "Agent Action Feedback",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Brief silent feedback after approving or denying an agent prompt"
            }

            notifManager.createNotificationChannel(blockedChannel)
            notifManager.createNotificationChannel(doneChannel)
            notifManager.createNotificationChannel(feedbackChannel)
        }
    }
}
