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
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage

class MyFirebaseMessagingService : FirebaseMessagingService() {

    override fun onNewToken(token: String) {
        super.onNewToken(token)
        Log.d("FCM", "New token generated: $token")
        
        // Save token to SharedPreferences so the app can register it with the bridge server
        val sharedPreferences = getSharedPreferences("AgentWatchPrefs", Context.MODE_PRIVATE)
        sharedPreferences.edit().putString("fcm_token", token).apply()
    }

    override fun onMessageReceived(remoteMessage: RemoteMessage) {
        super.onMessageReceived(remoteMessage)
        Log.d("FCM", "Received message from: ${remoteMessage.from}")

        // Extract title, body, and data payload
        val title = remoteMessage.data["title"] ?: remoteMessage.notification?.title ?: "Agent Alert"
        val body = remoteMessage.data["body"] ?: remoteMessage.notification?.body ?: "Agent details updated"
        val eventType = remoteMessage.data["event"] ?: "unknown"

        val cleanedBody = com.gabriel.agentwatch.util.MarkdownFormatter.clean(body)
        sendNotification(title, cleanedBody, eventType)
    }

    private fun sendNotification(title: String, messageBody: String, eventType: String) {
        val channelId = "agent_watch_channel"
        val notificationManager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager

        // Create notification channel (required for Android 8.0+)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                channelId,
                "Agent Watch Notifications",
                NotificationManager.IMPORTANCE_HIGH
            ).apply {
                description = "Shows real-time status and alerts from Claude Code"
                enableLights(true)
                enableVibration(true)
            }
            notificationManager.createNotificationChannel(channel)
        }

        // Base intent to open MainActivity when clicking the notification
        val mainIntent = Intent(this, com.gabriel.agentwatch.MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK
        }
        val mainPendingIntent = PendingIntent.getActivity(
            this, 0, mainIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        // Create the notification builder
        val builder = NotificationCompat.Builder(this, channelId)
            .setSmallIcon(android.R.drawable.ic_dialog_info) // System info icon
            .setContentTitle(title)
            .setContentText(messageBody)
            .setAutoCancel(true)
            .setContentIntent(mainPendingIntent)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setDefaults(NotificationCompat.DEFAULT_ALL)

        // Custom actions based on event type (e.g. PermissionRequest)
        if (eventType == "PermissionRequest") {
            // Allow Button ('y')
            val allowIntent = Intent(this, NotificationActionReceiver::class.java).apply {
                action = "com.gabriel.agentwatch.ACTION_ALLOW"
            }
            val allowPendingIntent = PendingIntent.getBroadcast(
                this, 1, allowIntent,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
            )

            // Deny Button ('n')
            val denyIntent = Intent(this, NotificationActionReceiver::class.java).apply {
                action = "com.gabriel.agentwatch.ACTION_DENY"
            }
            val denyPendingIntent = PendingIntent.getBroadcast(
                this, 2, denyIntent,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
            )

            // Reply/Dictate Action (inline text input)
            val replyLabel = "Dictate response..."
            val remoteInput = RemoteInput.Builder("KEY_TEXT_REPLY")
                .setLabel(replyLabel)
                .build()

            val replyIntent = Intent(this, NotificationActionReceiver::class.java).apply {
                action = "com.gabriel.agentwatch.ACTION_REPLY"
            }
            val replyPendingIntent = PendingIntent.getBroadcast(
                this, 3, replyIntent,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE // Must be mutable for RemoteInput
            )

            val replyAction = NotificationCompat.Action.Builder(
                android.R.drawable.ic_btn_speak_now,
                "Reply",
                replyPendingIntent
            ).addRemoteInput(remoteInput).build()

            builder.addAction(android.R.drawable.checkbox_on_background, "Allow", allowPendingIntent)
            builder.addAction(android.R.drawable.ic_delete, "Deny", denyPendingIntent)
            builder.addAction(replyAction)
        } else {
            // Add a simple "Reply" action to normal notifications so they can dictate a query/response
            val remoteInput = RemoteInput.Builder("KEY_TEXT_REPLY")
                .setLabel("Send input...")
                .build()

            val replyIntent = Intent(this, NotificationActionReceiver::class.java).apply {
                action = "com.gabriel.agentwatch.ACTION_REPLY"
            }
            val replyPendingIntent = PendingIntent.getBroadcast(
                this, 4, replyIntent,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE
            )

            val replyAction = NotificationCompat.Action.Builder(
                android.R.drawable.ic_btn_speak_now,
                "Send",
                replyPendingIntent
            ).addRemoteInput(remoteInput).build()

            builder.addAction(replyAction)
        }

        // Dispatch notification
        notificationManager.notify(1001, builder.build())
    }
}
