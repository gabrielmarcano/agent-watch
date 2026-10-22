package com.gabriel.agentwatch.mobile.push

import android.util.Log
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.network.PushMessage
import com.gabriel.agentwatch.network.PushRegistration
import com.gabriel.agentwatch.network.shouldShowBlocked
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage

/**
 * Receives the relay's data-only FCM pushes (contracts §4.1) and posts the phone's own chat
 * notifications. The app never uses FCM notification messages: everything is built locally and
 * setLocalOnly, so Android does not bridge it to the watch (which gets the same push itself).
 */
class MobileMessagingService : FirebaseMessagingService() {

    companion object {
        private const val TAG = "FCM"
    }

    override fun onNewToken(token: String) {
        Log.d(TAG, "New FCM token received (${token.length} chars)")
        PushRegistration.onToken(this, token)
    }

    override fun onMessageReceived(remoteMessage: RemoteMessage) {
        val message = PushMessage.parse(remoteMessage.data)
        Log.d(TAG, "Push: ${message.logSummary()}")

        val prefs = Prefs(this)
        when (message) {
            is PushMessage.Resolved -> {
                message.seq?.let { prefs.resolvedSeqs = prefs.resolvedSeqs.record(message.key, it) }
                MobileNotifications.onResolved(this, message)
            }
            is PushMessage.Blocked -> {
                if (!shouldShowBlocked(message.seq, prefs.resolvedSeqs.lastFor(message.key))) {
                    Log.d(TAG, "Dropping a stale blocked push for host=${message.host} pane=${message.paneId}")
                    return
                }
                MobileNotifications.showBlocked(this, message)
            }
            is PushMessage.Done -> MobileNotifications.showDone(this, message)
            is PushMessage.Digest -> MobileNotifications.showDigest(this, message)
            is PushMessage.Ignored -> return
        }
    }
}
