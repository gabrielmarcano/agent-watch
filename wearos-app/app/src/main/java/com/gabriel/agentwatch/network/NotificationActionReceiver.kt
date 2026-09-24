package com.gabriel.agentwatch.network

import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.RemoteInput
import com.gabriel.agentwatch.MainActivity
import com.gabriel.agentwatch.data.Prefs
import kotlinx.coroutines.*

class NotificationActionReceiver : BroadcastReceiver() {

    companion object {
        const val ACTION_ANSWER = "com.gabriel.agentwatch.ACTION_ANSWER"
        const val ACTION_CANCEL = "com.gabriel.agentwatch.ACTION_CANCEL"
        const val ACTION_PROMPT = "com.gabriel.agentwatch.ACTION_PROMPT"
        private const val TAG = "NotifActionReceiver"
    }

    override fun onReceive(context: Context, intent: Intent) {
        val action = intent.action ?: return
        val paneId = intent.getStringExtra("pane_id") ?: return
        val expectedSeq = intent.getLongExtra("state_change_seq", 0L)
        val notifId = intent.getIntExtra("notif_id", paneId.hashCode())

        Log.d(TAG, "Received notification action: $action for pane: $paneId, seq: $expectedSeq")

        val prefs = Prefs(context)
        if (!prefs.isPaired) {
            Log.e(TAG, "Not paired; cannot perform action")
            return
        }

        val client = RelayClient(prefs.relayUrl, prefs.deviceToken)
        val notifManager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager

        val pendingResult = goAsync()

        CoroutineScope(Dispatchers.IO).launch {
            try {
                withTimeout(10000) {
                    when (action) {
                        ACTION_ANSWER -> {
                            val optionId = intent.getStringExtra("option_id") ?: ""
                            val fingerprint = intent.getStringExtra("fingerprint") ?: ""
                            val result = client.answer(paneId, optionId, expectedSeq, fingerprint)
                            handleResult(context, notifManager, notifId, paneId, result, "Approved")
                        }
                        ACTION_CANCEL -> {
                            val result = client.cancel(paneId, expectedSeq)
                            handleResult(context, notifManager, notifId, paneId, result, "Denied")
                        }
                        ACTION_PROMPT -> {
                            val remoteInput = RemoteInput.getResultsFromIntent(intent)
                            val text = remoteInput?.getCharSequence("KEY_TEXT_REPLY")?.toString()
                            if (!text.isNullOrBlank()) {
                                val result = client.prompt(paneId, text, expectedSeq)
                                handleResult(context, notifManager, notifId, paneId, result, "Sent")
                            }
                        }
                    }
                }
            } catch (e: TimeoutCancellationException) {
                showFeedback(context, notifManager, notifId, paneId, "Could not reach the relay (timeout)", false)
            } catch (e: Exception) {
                showFeedback(context, notifManager, notifId, paneId, "Error: ${e.message}", false)
            } finally {
                pendingResult.finish()
            }
        }
    }

    private fun handleResult(
        context: Context,
        manager: NotificationManager,
        notifId: Int,
        paneId: String,
        result: Result<Unit>,
        successTitle: String
    ) {
        result.fold(
            onSuccess = {
                // Show brief silent feedback notification and auto-cancel
                showFeedback(context, manager, notifId, paneId, successTitle, isSuccess = true)
                CoroutineScope(Dispatchers.IO).launch {
                    delay(3000)
                    manager.cancel(notifId)
                }
            },
            onFailure = { error ->
                val isConflict = error is RelayError && error.httpStatus == 409
                if (isConflict) {
                    showFeedback(context, manager, notifId, paneId, "Changed — open the app", isSuccess = false)
                } else {
                    showFeedback(context, manager, notifId, paneId, "Could not reach the relay", isSuccess = false)
                }
            }
        )
    }

    private fun showFeedback(
        context: Context,
        manager: NotificationManager,
        notifId: Int,
        paneId: String,
        message: String,
        isSuccess: Boolean
    ) {
        val openIntent = Intent(context, MainActivity::class.java).apply {
            putExtra("pane_id", paneId)
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK
        }
        val openPending = PendingIntent.getActivity(
            context,
            notifId,
            openIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val channelId = if (isSuccess) "agent_watch_feedback" else "agent_blocked"
        val builder = NotificationCompat.Builder(context, channelId)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle("Agent Watch")
            .setContentText(message)
            .setAutoCancel(true)
            .setContentIntent(openPending)
            .setSilent(isSuccess)

        if (!isSuccess) {
            builder.addAction(android.R.drawable.ic_menu_view, "Open", openPending)
        }

        manager.notify(notifId, builder.build())
    }
}
