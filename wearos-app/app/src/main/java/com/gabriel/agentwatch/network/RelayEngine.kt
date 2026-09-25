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

/** Where the pairing is stored. Android: [com.gabriel.agentwatch.data.Prefs]; tests: in memory. */
interface RelayCredentials {
    val relayUrl: String
    val deviceToken: String?
    fun clearAuth()

    val isPaired: Boolean
        get() = relayUrl.isNotBlank() && !deviceToken.isNullOrBlank()
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

    fun getClient(): RelayClient? = synchronized(lock) { clientLocked() }

    private fun clientLocked(): RelayClient? {
        if (!credentials.isPaired) return null
        return client ?: RelayClient(credentials.relayUrl, credentials.deviceToken, http).also { client = it }
    }

    fun resetClient() = synchronized(lock) {
        client = null
        clientLocked()
        Unit
    }

    fun start() = synchronized(lock) {
        if (!credentials.isPaired) {
            state.update { it.copy(connection = Connection.Offline("Not paired")) }
            return
        }
        if (started) return
        started = true

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
        state.update { it.copy(connection = Connection.Offline(reason)) }
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
                reconnectAttempt = 0
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
                val reason = t?.message ?: "HTTP $statusCode"
                log("SSE onFailure: $reason (code: $statusCode)")
                if (statusCode == 401) {
                    credentials.clearAuth()
                    client = null
                    stopLocked("Authentication revoked")
                    store.clear()
                    state.value = UiState(connection = Connection.Offline("Authentication revoked"))
                    return@onStream
                }
                scheduleReconnectLocked(reason)
            }
        })
    }

    /** Runs [block] under the lock if [generation] is still the live stream. */
    private inline fun onStream(generation: Long, block: () -> Unit) = synchronized(lock) {
        if (generation == streamGeneration && started) block()
    }

    private fun scheduleReconnectLocked(reason: String) {
        if (!started) return
        state.update { it.copy(connection = Connection.Offline(reason)) }

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
                    state.update { it.copy(connection = Connection.Live) }
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
        val currentClient = getClient() ?: return
        coroutineScope {
            launch {
                val since = synchronized(lock) { store.beginFetch() }
                currentClient.agents().onSuccess { snapshot ->
                    synchronized(lock) {
                        store.applyFetched(snapshot, since)
                        publishAgentsLocked()
                    }
                }
            }
            launch {
                currentClient.history().onSuccess { items ->
                    state.update { it.copy(history = mergeHistory(it.history, items)) }
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
