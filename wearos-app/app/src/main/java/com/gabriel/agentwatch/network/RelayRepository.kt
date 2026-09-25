package com.gabriel.agentwatch.network

import android.content.Context
import android.util.Log
import com.gabriel.agentwatch.data.Prefs
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** [RelayCredentials] backed by the app's [Prefs]. */
private class PrefsCredentials(private val prefs: Prefs) : RelayCredentials {
    override val relayUrl: String get() = prefs.relayUrl
    override val deviceToken: String? get() = prefs.deviceToken
    override fun clearAuth() = prefs.clearAuth()
}

/**
 * Process-wide owner of the relay link and the live [state]. Android façade over [RelayEngine].
 *
 * Every 401 (stream, refresh, commands, notification actions, and clients built elsewhere once [init]
 * ran) ends in one state: token cleared, stream stopped, `state.auth == AuthState.REVOKED`, empty
 * lists. The UI must check [UiState.auth] before anything else and show pairing instead of
 * "Mac is offline" / "No active agents".
 */
object RelayRepository {
    private const val TAG = "RelayRepository"

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state.asStateFlow()

    @Volatile
    private var engine: RelayEngine? = null

    @Synchronized
    fun init(context: Context) {
        if (engine != null) return
        val appContext = context.applicationContext
        val prefs = Prefs(appContext)
        val newEngine = RelayEngine(
            state = _state,
            credentials = PrefsCredentials(prefs),
            hooks = object : RelayEngineHooks {
                // Reachable relay + accepted token: the moment to (re)send a pending FCM registration,
                // including right after pairing (PairingScreen restarts the engine).
                override fun onStreamOpened() = PushRegistration.ensure(appContext)

                // An agent that is no longer blocked must not keep an approval notification around.
                override fun onAgentsUpdated(update: AgentsUpdate) = ApprovalNotifications.onAgentsUpdated(appContext, update)
            },
            log = { Log.d(TAG, it) }
        )
        engine = newEngine
        // Clients built outside the repository (tile, complication, QuickDictate) report 401s here too.
        RelayClient.unauthorizedListener = newEngine::onUnauthorized
        // Complication and tile: refresh when what they show changes (throttled), not every 15 min.
        CoroutineScope(SupervisorJob() + Dispatchers.Default).launch {
            _state.collect { SurfaceUpdates.onState(appContext, it, prefs.pinnedPaneId) }
        }
    }

    fun getClient(): RelayClient? = engine?.getClient()

    /**
     * The stored pairing changed: closes the stream and drops the old pairing's client and data.
     * Kept for `PairingScreen` (`resetClient()` then `start()` is a full restart); new code calls [restart].
     */
    fun resetClient() {
        engine?.resetClient()
    }

    /** Opens the SSE stream if paired and not already running (idempotent). */
    fun start(context: Context) {
        init(context)
        engine?.start()
    }

    /** Reconnects from scratch with the pairing now in Prefs. Call after pairing or re-pairing. */
    fun restart(context: Context) {
        init(context)
        engine?.restart()
    }

    fun stop() {
        engine?.stop()
    }

    suspend fun refresh() {
        engine?.refresh()
    }

    private fun notPaired(): Result<Unit> =
        Result.failure(RelayError("not_paired", "Client not configured", 0))

    suspend fun answer(paneId: String, optionId: String, expectedSeq: Long, fingerprint: String): Result<Unit> =
        engine?.answer(paneId, optionId, expectedSeq, fingerprint) ?: notPaired()

    /** [fingerprint]: the fingerprint of the prompt being cancelled, when one is shown (see [RelayClient.cancel]). */
    suspend fun cancel(paneId: String, expectedSeq: Long, fingerprint: String? = null): Result<Unit> =
        engine?.cancel(paneId, expectedSeq, fingerprint) ?: notPaired()

    suspend fun prompt(paneId: String, text: String, expectedSeq: Long): Result<Unit> =
        engine?.prompt(paneId, text, expectedSeq) ?: notPaired()
}
