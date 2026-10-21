package com.gabriel.agentwatch.mobile.push

import android.app.KeyguardManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.core.app.RemoteInput
import com.gabriel.agentwatch.approval.FeedbackSurface
import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.network.RelayError
import com.gabriel.agentwatch.network.RelayRepository
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout

/**
 * Runs a notification's action against the relay. Approving actions (Allow, a question's answer)
 * are refused while the phone is locked, besides `setAuthenticationRequired` on the notification:
 * the phone is easier to pick up than a watch, so approving needs an unlock (phase 7 decision).
 * Deny, Cancel and a reply stay available: they do not grant a permission.
 */
class MobileNotificationActionReceiver : BroadcastReceiver() {

    companion object {
        private const val TAG = "NotifActionReceiver"

        /** Safety net over the command client's 8 s callTimeout; both end inside goAsync()'s ~10 s. */
        private const val ACTION_TIMEOUT_MS = 9_000L

        /** What the silent feedback notification says after the action succeeded. */
        fun successText(context: Context, action: String): String = context.getString(
            when (action) {
                MobileNotificationActions.ALLOW -> R.string.notification_approved
                MobileNotificationActions.DENY -> R.string.notification_denied
                MobileNotificationActions.ANSWER -> R.string.notification_answered
                MobileNotificationActions.CANCEL -> R.string.notification_canceled
                MobileNotificationActions.PROMPT -> R.string.notification_sent
                else -> R.string.notification_done
            }
        )
    }

    override fun onReceive(context: Context, intent: Intent) {
        val action = intent.action ?: return
        when (action) {
            MobileNotificationActions.REFRESH -> {
                MobileNotifications.refreshSummary(context)
                return
            }
            MobileNotificationActions.DISMISS_ALL -> {
                MobileNotifications.cancelAllChats(context)
                return
            }
        }

        val agent = MobileNotifications.agentOf(intent) ?: return
        val expectedSeq = intent.getLongExtra("state_change_seq", 0L)
        Log.d(TAG, "Notification action $action for host ${agent.host} pane ${agent.paneId} (seq $expectedSeq)")

        if (MobileNotificationActions.requiresUnlock(action) && isLocked(context)) {
            // No command: the prompt stays blocked and can be answered after unlocking, so leave
            // its chat notification in place.
            MobileNotifications.showFeedback(
                context, agent,
                context.getString(R.string.notification_unlock_to_approve),
                isSuccess = false,
                keepChat = true
            )
            return
        }

        RelayRepository.init(context)
        val client = RelayRepository.getClient()
        if (client == null) {
            MobileNotifications.showFeedback(
                context, agent,
                commandErrorFeedback(
                    RelayError("not_paired", "Client not configured", 0),
                    FeedbackSurface.NOTIFICATION
                ).message,
                isSuccess = false
            )
            return
        }

        val replyText = if (action == MobileNotificationActions.PROMPT) {
            RemoteInput.getResultsFromIntent(intent)?.getCharSequence("KEY_TEXT_REPLY")?.toString()
        } else {
            null
        }
        if (action == MobileNotificationActions.PROMPT && replyText.isNullOrBlank()) {
            // Keys only, never the text.
            Log.d(TAG, "Reply without text; extras keys=${intent.extras?.keySet()} clipData=${intent.clipData != null}")
            MobileNotifications.showFeedback(
                context, agent,
                context.getString(R.string.reply_empty),
                isSuccess = false,
                keepChat = true
            )
            return
        }

        val optionId = intent.getStringExtra("option_id").orEmpty()
        val fingerprint = intent.getStringExtra("fingerprint").orEmpty()
        val pendingResult = goAsync()

        CoroutineScope(Dispatchers.IO).launch {
            try {
                val result = withTimeout(ACTION_TIMEOUT_MS) {
                    when (action) {
                        MobileNotificationActions.ALLOW, MobileNotificationActions.DENY, MobileNotificationActions.ANSWER ->
                            client.answer(agent, optionId, expectedSeq, fingerprint)
                        MobileNotificationActions.CANCEL ->
                            client.cancel(agent, expectedSeq, fingerprint.takeIf { it.isNotBlank() })
                        MobileNotificationActions.PROMPT -> client.prompt(agent, replyText!!, expectedSeq)
                        else -> null
                    }
                } ?: return@launch
                result.fold(
                    onSuccess = {
                        MobileNotifications.showFeedback(
                            context, agent, successText(context, action), isSuccess = true
                        )
                    },
                    onFailure = { error ->
                        MobileNotifications.showFeedback(
                            context, agent,
                            commandErrorFeedback(error, FeedbackSurface.NOTIFICATION).message,
                            isSuccess = false
                        )
                    }
                )
            } catch (e: TimeoutCancellationException) {
                MobileNotifications.showFeedback(
                    context, agent,
                    commandErrorFeedback(e, FeedbackSurface.NOTIFICATION).message,
                    isSuccess = false
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Log.e(TAG, "Notification action failed: ${e.javaClass.simpleName}")
                MobileNotifications.showFeedback(
                    context, agent,
                    commandErrorFeedback(e, FeedbackSurface.NOTIFICATION).message,
                    isSuccess = false
                )
            } finally {
                pendingResult.finish()
            }
        }
    }

    private fun isLocked(context: Context): Boolean {
        val keyguard = context.getSystemService(Context.KEYGUARD_SERVICE) as? KeyguardManager ?: return false
        return keyguard.isDeviceLocked
    }
}
