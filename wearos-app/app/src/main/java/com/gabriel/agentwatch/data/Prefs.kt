package com.gabriel.agentwatch.data

import android.content.Context
import android.content.SharedPreferences
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.network.ResolvedSeqs

class Prefs(context: Context) : FcmRegistrationStore {
    private val prefs: SharedPreferences = context.getSharedPreferences("AgentWatchPrefs", Context.MODE_PRIVATE)

    init {
        // Purge legacy fields if present. `fcm_registered_token` was written even when registration
        // failed; the pairing-bound `fcm_registration` record replaces it.
        if (prefs.contains("local_ip") || prefs.contains("tailscale_ip") || prefs.contains("fcm_registered_token")) {
            prefs.edit()
                .remove("local_ip")
                .remove("tailscale_ip")
                .remove("fcm_registered_token")
                .apply()
        }
    }

    override var relayUrl: String
        get() = prefs.getString("relay_url", "") ?: ""
        set(value) = prefs.edit().putString("relay_url", value.trim()).apply()

    override var deviceToken: String?
        get() = prefs.getString("device_token", null)
        set(value) = prefs.edit().putString("device_token", value).apply()

    var deviceId: String?
        get() = prefs.getString("device_id", null)
        set(value) = prefs.edit().putString("device_id", value).apply()

    override var fcmToken: String?
        get() = prefs.getString("fcm_token", null)
        set(value) = prefs.edit().putString("fcm_token", value).apply()

    /** Written by [com.gabriel.agentwatch.network.FcmRegistrar] only after the relay accepted the token. */
    override var fcmRegistration: FcmRegistrationRecord?
        get() = FcmRegistrationRecord.decode(prefs.getString("fcm_registration", null))
        set(value) = prefs.edit().putString("fcm_registration", value?.encode()).apply()

    /**
     * The FCM token the relay has accepted for the **current** pairing, or null.
     *
     * Writes are ignored: registration is recorded by `FcmRegistrar` only after a 200, so a caller that
     * sets this after a registration that may have failed can no longer mark push as working.
     */
    var fcmRegisteredToken: String?
        get() {
            val record = fcmRegistration ?: return null
            return record.fcmToken.takeUnless { needsFcmRegistration(it, relayUrl, deviceToken, record) }
        }
        @Deprecated("Ignored. Use PushRegistration.ensure(context); it records the token only after the relay accepts it.")
        set(@Suppress("UNUSED_PARAMETER") value) {}

    /**
     * The agent set with "Pin to tile", or null. A pin saved before hosts has no host: `findAgent`
     * still finds its pane while only one host has it.
     */
    var pinnedTarget: AgentKey?
        get() = prefs.getString("pinned_pane_id", null)?.takeIf { it.isNotBlank() }
            ?.let { AgentKey(prefs.getString("pinned_host", null).orEmpty(), it) }
        set(value) = prefs.edit()
            .putString("pinned_pane_id", value?.paneId)
            .putString("pinned_host", value?.host)
            .apply()

    /** Last `resolved` push seq per agent (contracts §4.1): late `blocked` pushes at or below it are stale. */
    var resolvedSeqs: ResolvedSeqs
        get() = ResolvedSeqs.decode(prefs.getString("resolved_seqs", null))
        set(value) = prefs.edit().putString("resolved_seqs", value.encode()).apply()

    val isPaired: Boolean
        get() = !relayUrl.isBlank() && !deviceToken.isNullOrBlank()

    fun clearAuth() {
        prefs.edit()
            .remove("device_token")
            .remove("device_id")
            .remove("fcm_registration")
            .remove("resolved_seqs")
            .apply()
    }
}
