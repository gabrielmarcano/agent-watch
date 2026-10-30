package com.gabriel.agentwatch.mobile.push

import android.content.Context
import android.util.Log
import com.gabriel.agentwatch.network.PushRegistration
import com.google.firebase.FirebaseApp
import com.google.firebase.messaging.FirebaseMessaging

/**
 * FCM on the phone. The Firebase config (`mobile/google-services.json`, the owner's file, never
 * committed) is optional so the app still builds without it: push then stays off, everything else
 * works. With it, the token is fetched once per start and registered like the watch's.
 */
object MobilePush {
    private const val TAG = "FCM"

    fun available(context: Context): Boolean = try {
        FirebaseApp.getApps(context).isNotEmpty()
    } catch (e: IllegalStateException) {
        Log.w(TAG, "Firebase is not configured: ${e.javaClass.simpleName}")
        false
    }

    /** Fetches the token; onNewToken stores and registers it. No-op without a Firebase config. */
    fun fetchToken(context: Context) {
        if (!available(context)) return
        try {
            FirebaseMessaging.getInstance().token.addOnCompleteListener { task ->
                if (task.isSuccessful) {
                    task.result?.takeIf { it.isNotBlank() }?.let { PushRegistration.onToken(context.applicationContext, it) }
                } else {
                    Log.w(TAG, "Fetching the FCM token failed: ${task.exception?.javaClass?.simpleName}")
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "FirebaseMessaging is unavailable: ${e.javaClass.simpleName}")
        }
    }

    /** Re-registers a stored token if the relay lost it (best effort, retried by [PushRegistration]). */
    fun ensure(context: Context) {
        if (available(context)) PushRegistration.ensure(context)
    }
}
