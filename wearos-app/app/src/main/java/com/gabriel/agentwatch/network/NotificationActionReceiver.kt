package com.gabriel.agentwatch.network

import android.app.NotificationManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.RemoteInput
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.approval.FeedbackSurface
import com.gabriel.agentwatch.approval.commandErrorFeedback
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout

class NotificationActionReceiver : BroadcastReceiver() {

    companion object {
        const val ACTION_ANSWER = "com.gabriel.agentwatch.ACTION_ANSWER"
        const val ACTION_CANCEL = "com.gabriel.agentwatch.ACTION_CANCEL"
        const val ACTION_PROMPT = "com.gabriel.agentwatch.ACTION_PROMPT"
        private const val TAG = "NotifActionReceiver"

        /** Safety net over the command client's 8 s callTimeout; both end well inside goAsync()'s ~10 s. */
        private const val ACTION_TIMEOUT_MS = 9_000L
        /** How long a success confirmation stays before the system removes it. */
        private const val SUCCESS_FEEDBACK_MS = 3_000L

        /** A Reply action that carries no text: shown as an error, never sent. */
        fun isBlankReply(action: String, replyText: String?): Boolean =
            action == ACTION_PROMPT && replyText.isNullOrBlank()
    }

    override fun onReceive(context: Context, intent: Intent) {
        val action = intent.action ?: return
        val paneId = intent.getStringExtra("pane_id") ?: return
        val expectedSeq = intent.getLongExtra("state_change_seq", 0L)
        val notifId = intent.getIntExtra("notif_id", AgentNotifications.idForPane(paneId))

        Log.d(TAG, "Notification action $action for pane $paneId (seq $expectedSeq)")

        val notifManager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        NotificationChannels.ensure(context)

        // The repository's client: a 401 here revokes the pairing like anywhere else in the app.
        RelayRepository.init(context)
        val client = RelayRepository.getClient()
        if (client == null) {
            Log.w(TAG, "Not paired; cannot perform action")
            val message = commandErrorFeedback(RelayError("not_paired", "Client not configured", 0), FeedbackSurface.NOTIFICATION).message
            showFeedback(context, notifManager, notifId, paneId, message, isSuccess = false)
            return
        }

        val replyText = if (action == ACTION_PROMPT) {
            RemoteInput.getResultsFromIntent(intent)?.getCharSequence("KEY_TEXT_REPLY")?.toString()
        } else {
            null
        }
        if (isBlankReply(action, replyText)) {
            // Never a silent drop: the reply's notification says nothing was sent. Keys only, never text.
            Log.d(TAG, "Reply without text; extras keys=${intent.extras?.keySet()} clipData=${intent.clipData != null}")
            showFeedback(context, notifManager, notifId, paneId, context.getString(R.string.reply_empty), isSuccess = false)
            return
        }

        val isDeny = intent.getBooleanExtra("is_deny", false)
        val isChoice = intent.getBooleanExtra("is_choice", false)
        val pendingResult = goAsync()

        CoroutineScope(Dispatchers.IO).launch {
            try {
                val result = withTimeout(ACTION_TIMEOUT_MS) {
                    when (action) {
                        ACTION_ANSWER -> {
                            val optionId = intent.getStringExtra("option_id") ?: ""
                            val fingerprint = intent.getStringExtra("fingerprint") ?: ""
                            client.answer(paneId, optionId, expectedSeq, fingerprint)
                        }
                        ACTION_CANCEL -> {
                            // The push's fingerprint, when present: the bridge refuses the cancel if the menu changed.
                            val fingerprint = intent.getStringExtra("fingerprint")?.takeIf { it.isNotBlank() }
                            client.cancel(paneId, expectedSeq, fingerprint)
                        }
                        ACTION_PROMPT -> client.prompt(paneId, replyText!!, expectedSeq)
                        else -> null
                    }
                } ?: return@launch
                result.fold(
                    onSuccess = {
                        showFeedback(context, notifManager, notifId, paneId, actionSuccessTitle(action, isDeny, isChoice), isSuccess = true)
                    },
                    onFailure = { error ->
                        // Same mapping as the app screen (contracts §2.4), worded for a notification.
                        val message = commandErrorFeedback(error, FeedbackSurface.NOTIFICATION).message
                        showFeedback(context, notifManager, notifId, paneId, message, isSuccess = false)
                    }
                )
            } catch (e: TimeoutCancellationException) {
                // Our own safety net fired: "Relay timed out" (the command may still have run).
                val message = commandErrorFeedback(e, FeedbackSurface.NOTIFICATION).message
                showFeedback(context, notifManager, notifId, paneId, message, isSuccess = false)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Log.e(TAG, "Notification action failed: ${e.javaClass.simpleName}")
                val message = commandErrorFeedback(e, FeedbackSurface.NOTIFICATION).message
                showFeedback(context, notifManager, notifId, paneId, message, isSuccess = false)
            } finally {
                pendingResult.finish()
            }
        }
    }

    /**
     * Replaces the pane's notification with a short result on the low-importance feedback channel, so
     * neither success nor failure vibrates again. Success removes itself after [SUCCESS_FEEDBACK_MS]
     * (system timer, not a coroutine that outlives goAsync()); failure stays with an Open action.
     */
    private fun showFeedback(
        context: Context,
        manager: NotificationManager,
        notifId: Int,
        paneId: String,
        message: String,
        isSuccess: Boolean
    ) {
        val openPending = NotificationIntents.openApp(context, paneId)

        val builder = NotificationCompat.Builder(context, NotificationChannels.FEEDBACK)
            .setSmallIcon(R.drawable.ic_agent)
            .setContentTitle("Agent Watch")
            .setContentText(message)
            .setAutoCancel(true)
            .setContentIntent(openPending)
            .setSilent(true)
            .setOnlyAlertOnce(true)
            .addExtras(NotificationIntents.tag(AgentNotifications.KIND_FEEDBACK, paneId))

        if (isSuccess) {
            builder.setTimeoutAfter(SUCCESS_FEEDBACK_MS)
        } else {
            builder.addAction(R.drawable.ic_agent, "Open", openPending)
        }

        manager.notify(notifId, builder.build())
    }
}
