package com.gabriel.agentwatch.data

import android.content.Context
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.network.ResolvedSeqs

class Prefs(private val store: KeyValueStore) : FcmRegistrationStore {
    constructor(context: Context) : this(
        SharedPrefsKeyValueStore(context.getSharedPreferences("AgentWatchPrefs", Context.MODE_PRIVATE))
    )

    init {
        // Purge legacy fields if present. `fcm_registered_token` was written even when registration
        // failed; the pairing-bound `fcm_registration` record replaces it.
        if (store.contains("local_ip") || store.contains("tailscale_ip") || store.contains("fcm_registered_token")) {
            store.remove("local_ip")
            store.remove("tailscale_ip")
            store.remove("fcm_registered_token")
        }
    }

    override var relayUrl: String
        get() = store.getString("relay_url", "") ?: ""
        set(value) = store.putString("relay_url", value.trim())

    override var deviceToken: String?
        get() = store.getString("device_token", null)
        set(value) = store.putString("device_token", value)

    var deviceId: String?
        get() = store.getString("device_id", null)
        set(value) = store.putString("device_id", value)

    override var fcmToken: String?
        get() = store.getString("fcm_token", null)
        set(value) = store.putString("fcm_token", value)

    /** Written by [com.gabriel.agentwatch.network.FcmRegistrar] only after the relay accepted the token. */
    override var fcmRegistration: FcmRegistrationRecord?
        get() = FcmRegistrationRecord.decode(store.getString("fcm_registration", null))
        set(value) = store.putString("fcm_registration", value?.encode())

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
        get() = store.getString("pinned_pane_id", null)?.takeIf { it.isNotBlank() }
            ?.let { AgentKey(store.getString("pinned_host", null).orEmpty(), it) }
        set(value) {
            store.putString("pinned_pane_id", value?.paneId)
            store.putString("pinned_host", value?.host)
        }

    /** Last `resolved` push seq per agent (contracts §4.1): late `blocked` pushes at or below it are stale. */
    var resolvedSeqs: ResolvedSeqs
        get() = synchronized(resolvedLock) {
            ResolvedSeqs.decode(store.getString("resolved_seqs", null))
        }
        set(value) = synchronized(resolvedLock) {
            store.putString("resolved_seqs", value.encode())
        }

    /**
     * Atomically records [seq] for [agent] into stored `resolved_seqs`, ensuring concurrent
     * callers (e.g. FCM push handler and UI / broadcast receivers) do not clobber each other.
     * Returns the updated [ResolvedSeqs].
     */
    fun recordResolvedSeq(agent: AgentKey, seq: Long): ResolvedSeqs = synchronized(resolvedLock) {
        val current = ResolvedSeqs.decode(store.getString("resolved_seqs", null))
        val updated = current.record(agent, seq)
        store.putString("resolved_seqs", updated.encode())
        updated
    }

    val isPaired: Boolean
        get() = !relayUrl.isBlank() && !deviceToken.isNullOrBlank()

    fun clearAuth() {
        synchronized(resolvedLock) {
            store.remove("device_token")
            store.remove("device_id")
            store.remove("fcm_registration")
            store.remove("resolved_seqs")
        }
    }

    companion object {
        private val resolvedLock = Any()
    }
}
