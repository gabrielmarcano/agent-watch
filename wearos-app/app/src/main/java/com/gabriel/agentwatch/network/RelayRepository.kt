package com.gabriel.agentwatch.network

import android.content.Context
import android.util.Log
import com.gabriel.agentwatch.data.Prefs
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** [RelayCredentials] backed by the app's [Prefs]. */
private class PrefsCredentials(private val prefs: Prefs) : RelayCredentials {
    override val relayUrl: String get() = prefs.relayUrl
    override val deviceToken: String? get() = prefs.deviceToken
    override fun clearAuth() = prefs.clearAuth()
}

/**
 * Process-wide owner of the relay link and the live [state]. Android façade over [RelayEngine].
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
        engine = RelayEngine(
            state = _state,
            credentials = PrefsCredentials(Prefs(appContext)),
            log = { Log.d(TAG, it) }
        )
    }

    fun getClient(): RelayClient? = engine?.getClient()

    fun resetClient() {
        engine?.resetClient()
    }

    fun start(context: Context) {
        init(context)
        engine?.start()
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
