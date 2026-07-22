package com.gabriel.agentwatch.network

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.core.app.RemoteInput

class NotificationActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val action = intent.action
        val sharedPreferences = context.getSharedPreferences("AgentWatchPrefs", Context.MODE_PRIVATE)
        val localIp = sharedPreferences.getString("local_ip", "192.168.1.20") ?: "192.168.1.20"
        val tailscaleIp = sharedPreferences.getString("tailscale_ip", "100.64.0.1") ?: "100.64.0.1"

        Log.d("NotificationReceiver", "Received action: $action")

        val textToSend = when (action) {
            "com.gabriel.agentwatch.ACTION_ALLOW" -> "y"
            "com.gabriel.agentwatch.ACTION_DENY" -> "n"
            "com.gabriel.agentwatch.ACTION_REPLY" -> {
                val remoteInput = RemoteInput.getResultsFromIntent(intent)
                remoteInput?.getCharSequence("KEY_TEXT_REPLY")?.toString()
            }
            else -> null
        }

        if (!textToSend.isNullOrEmpty()) {
            val client = SseClient(localIp, tailscaleIp)
            client.sendInputCommand(textToSend) { success ->
                Log.d("NotificationReceiver", "Sent inline reply '$textToSend' with success: $success")
            }
        }
    }
}
