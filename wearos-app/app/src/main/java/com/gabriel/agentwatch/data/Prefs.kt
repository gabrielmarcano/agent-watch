package com.gabriel.agentwatch.data

import android.content.Context
import android.content.SharedPreferences

class Prefs(context: Context) {
    private val prefs: SharedPreferences = context.getSharedPreferences("AgentWatchPrefs", Context.MODE_PRIVATE)

    init {
        // Purge legacy fields if present
        if (prefs.contains("local_ip") || prefs.contains("tailscale_ip")) {
            prefs.edit()
                .remove("local_ip")
                .remove("tailscale_ip")
                .apply()
        }
    }

    var relayUrl: String
        get() = prefs.getString("relay_url", "") ?: ""
        set(value) = prefs.edit().putString("relay_url", value.trim()).apply()

    var deviceToken: String?
        get() = prefs.getString("device_token", null)
        set(value) = prefs.edit().putString("device_token", value).apply()

    var deviceId: String?
        get() = prefs.getString("device_id", null)
        set(value) = prefs.edit().putString("device_id", value).apply()

    var fcmToken: String?
        get() = prefs.getString("fcm_token", null)
        set(value) = prefs.edit().putString("fcm_token", value).apply()

    var fcmRegisteredToken: String?
        get() = prefs.getString("fcm_registered_token", null)
        set(value) = prefs.edit().putString("fcm_registered_token", value).apply()

    var pinnedPaneId: String?
        get() = prefs.getString("pinned_pane_id", null)
        set(value) = prefs.edit().putString("pinned_pane_id", value).apply()

    val isPaired: Boolean
        get() = !relayUrl.isBlank() && !deviceToken.isNullOrBlank()

    fun clearAuth() {
        prefs.edit()
            .remove("device_token")
            .remove("device_id")
            .remove("fcm_registered_token")
            .apply()
    }
}
