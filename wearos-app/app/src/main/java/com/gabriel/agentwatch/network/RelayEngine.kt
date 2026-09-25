package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.data.AgentStore
import com.gabriel.agentwatch.data.mergeHistory
import com.gabriel.agentwatch.model.*
import com.google.gson.Gson
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.update
import okhttp3.Response
import okhttp3.sse.EventSource
import okhttp3.sse.EventSourceListener
import java.net.SocketTimeoutException

/** Where the pairing is stored. Android: [com.gabriel.agentwatch.data.Prefs]; tests: in memory. */
interface RelayCredentials {
    val relayUrl: String
    val deviceToken: String?
    fun clearAuth()

    val isPaired: Boolean
        get() = relayUrl.isNotBlank() && !deviceToken.isNullOrBlank()
}

/** Side effects the engine triggers but does not own. Called outside the engine's lock. */
interface RelayEngineHooks {
    /** The relay rejected the stored token (401); the credentials are already cleared. */
    fun onAuthRevoked() {}
}

/**
 * The relay link without Android: SSE lifecycle and reconnect, the merged agent/history state, and the
 * commands. [RelayRepository] is the process-wide Android façade over one instance.
 *
 * Thread-safety: every mutable field is guarded by [lock]; SSE callbacks arrive on OkHttp threads.
 */
class RelayEngine(
    private val state: MutableStateFlow<UiState>,
    private val credentials: RelayCredentials,
    private val http: RelayHttpClients = RelayHttpClients.shared,
    private val hooks: RelayEngineHooks = object : RelayEngineHooks {},
    private val reconnectDelayMs: (attempt: Int) -> Long = ::defaultReconnectDelayMs,
    private val dispatcher: CoroutineDispatcher = Dispatchers.IO,
    private val log: (String) -> Unit = {}
) {
    private val gson = Gson()
    private val lock = Any()

    private val store = AgentStore()
    private var client: RelayClient? = null
    private var eventSource: EventSource? = null
    private var reconnectJob: Job? = null
    private var scope: CoroutineScope? = null
    private var reconnectAttempt = 0
    private var started = false
    /** Bumped whenever a stream is replaced or stopped; callbacks from older streams are ignored. */
    private var streamGeneration = 0L
    /** Bumped when the pairing changes; results of requests made under an older pairing are dropped. */
    private var pairingEpoch = 0L

    /** A client for the stored pairing, or null when not paired. Rebuilt when the stored URL or token changes. */
    fun getClient(): RelayClient? = synchronized(lock) { clientLocked() }

    private fun clientLocked(): RelayClient? {
        if (!credentials.isPaired) return null
        val url = credentials.relayUrl
        val token = credentials.deviceToken
        val current = client
        if (current != null && current.baseUrl == url && current.token == token) return current
        return RelayClient(url, token, http, onUnauthorized = ::onUnauthorized).also { client = it }
    }

    /**
     * The stored pairing changed (new relay URL or token): closes the stream, drops the client and every
     * agent/history item of the old pairing, and forgets in-flight results. Follow with [start], or call
     * [restart] which does both.
     */
    fun resetClient() = synchronized(lock) {
        stopLocked("Disconnected")
        pairingEpoch++
        client = null
        store.clear()
        state.value = UiState(connection = Connection.Connecting, auth = authWhenStoppedLocked())
    }

    /** PAIRED if a token is stored; otherwise REVOKED stays REVOKED (the UI explains why), else UNPAIRED. */
    private fun authWhenStoppedLocked(): AuthState = when {
        credentials.isPaired -> AuthState.PAIRED
        state.value.auth == AuthState.REVOKED -> AuthState.REVOKED
        else -> AuthState.UNPAIRED
    }

    /**
     * The relay rejected [token] with 401 (any request or the stream). If it is still the stored token,
     * the pairing is revoked: token cleared, stream stopped, no reconnect, agents and history dropped,
     * and [UiState.auth] = [AuthState.REVOKED]. A 401 for an older token (re-paired meanwhile) is ignored.
     */
    fun onUnauthorized(token: String) {
        synchronized(lock) {
            if (token.isBlank() || credentials.deviceToken != token) return
            log("Relay rejected the device token (401): pairing revoked")
            credentials.clearAuth()
            stopLocked("Session expired")
            pairingEpoch++
            client = null
            store.clear()
            state.value = UiState(connection = Connection.Offline("Session expired"), auth = AuthState.REVOKED)
        }
        hooks.onAuthRevoked()
    }

    /** Reconnects from scratch with the stored pairing (call after pairing or re-pairing). */
    fun restart() = synchronized(lock) {
        resetClient()
        start()
    }

    fun start() = synchronized(lock) {
        if (!credentials.isPaired) {
            val auth = authWhenStoppedLocked()
            state.update { it.copy(connection = Connection.Offline("Not paired"), stale = true, auth = auth) }
            return
        }
        if (started) return
        started = true
        state.update { it.copy(auth = AuthState.PAIRED) }

        val newScope = CoroutineScope(SupervisorJob() + dispatcher)
        scope = newScope
        connectLocked()
        newScope.launch { refresh() }
        Unit
    }

    fun stop() = synchronized(lock) {
        stopLocked("Disconnected")
    }

    private fun stopLocked(reason: String) {
        started = false
        streamGeneration++
        reconnectJob?.cancel()
        reconnectJob = null
        eventSource?.cancel()
        eventSource = null
        scope?.cancel()
        scope = null
        reconnectAttempt = 0
        state.update { it.copy(connection = Connection.Offline(reason), stale = true) }
    }

    private fun connectLocked() {
        if (!started) return
        val currentClient = clientLocked() ?: return
        val generation = ++streamGeneration

        state.update { it.copy(connection = Connection.Connecting) }
        eventSource?.cancel()
        eventSource = currentClient.events(object : EventSourceListener() {
            override fun onOpen(eventSource: EventSource, response: Response) = onStream(generation) {
                log("SSE onOpen")
                state.update { it.copy(connection = Connection.Live) }
            }

            override fun onEvent(eventSource: EventSource, id: String?, type: String?, data: String) = onStream(generation) {
                handleEventLocked(type ?: "", data)
            }

            override fun onClosed(eventSource: EventSource) = onStream(generation) {
                log("SSE onClosed")
                scheduleReconnectLocked("Connection closed")
            }

            override fun onFailure(eventSource: EventSource, t: Throwable?, response: Response?) = onStream(generation) {
                val statusCode = response?.code ?: 0
                val reason = when {
                    t is SocketTimeoutException -> "No data from the relay"
                    t != null -> t.message ?: t.javaClass.simpleName
                    else -> "HTTP $statusCode"
                }
                log("SSE onFailure: $reason (code: $statusCode)")
                // A 401 was already reported by RelayClient.events → onUnauthorized; never retry with that token.
                if (statusCode == 401) return@onStream
                scheduleReconnectLocked(reason)
            }
        })
    }

    /** Runs [block] under the lock if [generation] is still the live stream. */
    private inline fun onStream(generation: Long, block: () -> Unit) = synchronized(lock) {
        if (generation == streamGeneration && started) block()
    }

    /** The stream is gone (closed, failed or silent): the list is now stale until the next snapshot. */
    private fun scheduleReconnectLocked(reason: String) {
        if (!started) return
        streamGeneration++ // late callbacks from the dead stream are ignored
        eventSource?.cancel()
        eventSource = null
        state.update { it.copy(connection = Connection.Offline(reason), stale = true) }

        val delayMs = reconnectDelayMs(reconnectAttempt++)
        log("Reconnecting SSE in ${delayMs}ms")
        reconnectJob?.cancel()
        reconnectJob = scope?.launch {
            delay(delayMs)
            synchronized(lock) { connectLocked() }
        }
    }

    private fun publishAgentsLocked() {
        val agents = store.agents()
        val hostOnline = store.hostOnline
        val herdrOnline = store.herdrOnline
        state.update { it.copy(agents = agents, hostOnline = hostOnline, herdrOnline = herdrOnline) }
    }

    private fun handleEventLocked(type: String, data: String) {
        try {
            when (type) {
                "snapshot" -> {
                    store.applySnapshot(gson.fromJson(data, AgentsSnapshot::class.java))
                    publishAgentsLocked()
                    reconnectAttempt = 0 // only a stream that delivered data resets the backoff
                    state.update { it.copy(connection = Connection.Live, stale = false) }
                }
                "agent" -> {
                    if (store.applyAgent(gson.fromJson(data, AgentState::class.java))) publishAgentsLocked()
                }
                "agent_removed" -> {
                    store.applyRemoved(gson.fromJson(data, PaneRef::class.java).pane_id)
                    publishAgentsLocked()
                }
                "host" -> {
                    store.applyHost(gson.fromJson(data, HostEvent::class.java))
                    publishAgentsLocked()
                }
                "history" -> {
                    val item = gson.fromJson(data, HistoryItem::class.java)
                    state.update { it.copy(history = mergeHistory(it.history, listOf(item))) }
                }
            }
        } catch (e: Exception) {
            log("Failed to parse SSE event $type: ${e.message}")
        }
    }

    suspend fun refresh() {
        val (currentClient, epoch) = synchronized(lock) { (clientLocked() ?: return) to pairingEpoch }
        coroutineScope {
            launch {
                val since = synchronized(lock) { store.beginFetch() }
                currentClient.agents().onSuccess { snapshot ->
                    synchronized(lock) {
                        if (epoch != pairingEpoch) return@onSuccess
                        store.applyFetched(snapshot, since)
                        publishAgentsLocked()
                    }
                }
            }
            launch {
                currentClient.history().onSuccess { items ->
                    synchronized(lock) {
                        if (epoch != pairingEpoch) return@onSuccess
                        state.update { it.copy(history = mergeHistory(it.history, items)) }
                    }
                }
            }
        }
    }

    private fun notPaired(): Result<Unit> =
        Result.failure(RelayError("not_paired", "Client not configured", 0))

    /** Re-fetches after a success, and after an error that means our view is stale (see commandErrorFeedback). */
    private suspend fun refreshAfter(res: Result<Unit>): Result<Unit> {
        val err = res.exceptionOrNull()
        if (err == null || commandErrorFeedback(err).refresh) {
            refresh()
        }
        return res
    }

    suspend fun answer(paneId: String, optionId: String, expectedSeq: Long, fingerprint: String): Result<Unit> {
        val currentClient = getClient() ?: return notPaired()
        return refreshAfter(currentClient.answer(paneId, optionId, expectedSeq, fingerprint))
    }

    suspend fun cancel(paneId: String, expectedSeq: Long, fingerprint: String? = null): Result<Unit> {
        val currentClient = getClient() ?: return notPaired()
        return refreshAfter(currentClient.cancel(paneId, expectedSeq, fingerprint))
    }

    suspend fun prompt(paneId: String, text: String, expectedSeq: Long): Result<Unit> {
        val currentClient = getClient() ?: return notPaired()
        return refreshAfter(currentClient.prompt(paneId, text, expectedSeq))
    }
}

/** SSE reconnect backoff (contracts §2.3): 1 s, 2 s, 4 s … capped at 30 s. */
fun defaultReconnectDelayMs(attempt: Int): Long =
    (1_000L shl attempt.coerceIn(0, 5)).coerceAtMost(30_000L)
