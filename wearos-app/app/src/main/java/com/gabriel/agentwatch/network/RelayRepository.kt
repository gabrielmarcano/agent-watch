package com.gabriel.agentwatch.network

import android.content.Context
import android.util.Log
import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.model.*
import com.google.gson.Gson
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import okhttp3.Response
import okhttp3.sse.EventSource
import okhttp3.sse.EventSourceListener

sealed class Connection {
    object Connecting : Connection()
    object Live : Connection()
    data class Offline(val reason: String) : Connection()
}

data class UiState(
    val connection: Connection = Connection.Connecting,
    val hostOnline: Boolean = false,
    val herdrOnline: Boolean = false,
    val agents: List<AgentState> = emptyList(),
    val history: List<HistoryItem> = emptyList()
)

object RelayRepository {
    private const val TAG = "RelayRepository"
    private val gson = Gson()

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state.asStateFlow()

    private var eventSource: EventSource? = null
    private var reconnectJob: Job? = null
    private var coroutineScope: CoroutineScope? = null

    private var appContext: Context? = null
    private var prefs: Prefs? = null
    private var client: RelayClient? = null

    private var backoffSeconds = 1L
    private var isStarted = false

    fun init(context: Context) {
        if (appContext == null) {
            appContext = context.applicationContext
            prefs = Prefs(context.applicationContext)
        }
    }

    fun getClient(): RelayClient? {
        val p = prefs ?: return null
        if (!p.isPaired) return null
        if (client == null) {
            client = RelayClient(p.relayUrl, p.deviceToken)
        }
        return client
    }

    fun resetClient() {
        client = null
        val p = prefs ?: return
        if (p.isPaired) {
            client = RelayClient(p.relayUrl, p.deviceToken)
        }
    }

    @Synchronized
    fun start(context: Context) {
        init(context)
        val p = prefs ?: return
        if (!p.isPaired) {
            _state.update { it.copy(connection = Connection.Offline("Not paired")) }
            return
        }

        if (isStarted) return
        isStarted = true

        coroutineScope = CoroutineScope(Dispatchers.IO + SupervisorJob())
        connectSse()
        coroutineScope?.launch {
            refresh()
        }
    }

    @Synchronized
    fun stop() {
        isStarted = false
        reconnectJob?.cancel()
        reconnectJob = null
        eventSource?.cancel()
        eventSource = null
        coroutineScope?.cancel()
        coroutineScope = null
        _state.update { it.copy(connection = Connection.Offline("Disconnected")) }
    }

    private fun connectSse() {
        if (!isStarted) return
        val currentClient = getClient() ?: return

        _state.update { it.copy(connection = Connection.Connecting) }

        eventSource?.cancel()
        eventSource = currentClient.events(object : EventSourceListener() {
            override fun onOpen(eventSource: EventSource, response: Response) {
                Log.d(TAG, "SSE onOpen")
                backoffSeconds = 1L
                _state.update { it.copy(connection = Connection.Live) }
            }

            override fun onEvent(eventSource: EventSource, id: String?, type: String?, data: String) {
                handleEvent(type ?: "", data)
            }

            override fun onClosed(eventSource: EventSource) {
                Log.d(TAG, "SSE onClosed")
                scheduleReconnect("Connection closed")
            }

            override fun onFailure(eventSource: EventSource, t: Throwable?, response: Response?) {
                val statusCode = response?.code ?: 0
                val reason = t?.message ?: "HTTP $statusCode"
                Log.w(TAG, "SSE onFailure: $reason (code: $statusCode)")

                if (statusCode == 401) {
                    Log.e(TAG, "Relay returned 401: device token revoked or invalid")
                    prefs?.clearAuth()
                    resetClient()
                    stop()
                    _state.update { UiState(connection = Connection.Offline("Authentication revoked")) }
                    return
                }

                scheduleReconnect(reason)
            }
        })
    }

    private fun scheduleReconnect(reason: String) {
        if (!isStarted) return
        _state.update { it.copy(connection = Connection.Offline(reason)) }

        reconnectJob?.cancel()
        reconnectJob = coroutineScope?.launch {
            val delayMs = backoffSeconds * 1000L
            Log.d(TAG, "Reconnecting SSE in ${backoffSeconds}s...")
            delay(delayMs)
            backoffSeconds = (backoffSeconds * 2).coerceAtMost(30L)
            if (isStarted) {
                connectSse()
            }
        }
    }

    private fun handleEvent(type: String, data: String) {
        try {
            when (type) {
                "snapshot" -> {
                    val snapshot = gson.fromJson(data, AgentsSnapshot::class.java)
                    val sorted = snapshot.agents.sortedWith(
                        compareByDescending<AgentState> { it.severity() }
                            .thenBy { it.label.lowercase() }
                    )
                    _state.update {
                        it.copy(
                            connection = Connection.Live,
                            hostOnline = snapshot.host_online,
                            herdrOnline = snapshot.herdr_online,
                            agents = sorted
                        )
                    }
                }
                "agent" -> {
                    val agent = gson.fromJson(data, AgentState::class.java)
                    _state.update { current ->
                        val updated = current.agents.filterNot { it.pane_id == agent.pane_id }.toMutableList()
                        updated.add(agent)
                        val sorted = updated.sortedWith(
                            compareByDescending<AgentState> { it.severity() }
                                .thenBy { it.label.lowercase() }
                        )
                        current.copy(agents = sorted)
                    }
                }
                "agent_removed" -> {
                    val ref = gson.fromJson(data, PaneRef::class.java)
                    _state.update { current ->
                        current.copy(agents = current.agents.filterNot { it.pane_id == ref.pane_id })
                    }
                }
                "host" -> {
                    val host = gson.fromJson(data, HostEvent::class.java)
                    _state.update {
                        it.copy(
                            hostOnline = host.host_online,
                            herdrOnline = host.herdr_online
                        )
                    }
                }
                "history" -> {
                    val item = gson.fromJson(data, HistoryItem::class.java)
                    _state.update { current ->
                        val filtered = current.history.filterNot { it.id == item.id }
                        val combined = (listOf(item) + filtered).take(200)
                        current.copy(history = combined)
                    }
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "Failed to parse SSE event $type: ${e.message}")
        }
    }

    suspend fun refresh() {
        val currentClient = getClient() ?: return
        coroutineScope {
            launch {
                val snapRes = currentClient.agents()
                snapRes.onSuccess { snapshot ->
                    val sorted = snapshot.agents.sortedWith(
                        compareByDescending<AgentState> { it.severity() }
                            .thenBy { it.label.lowercase() }
                    )
                    _state.update {
                        it.copy(
                            hostOnline = snapshot.host_online,
                            herdrOnline = snapshot.herdr_online,
                            agents = sorted
                        )
                    }
                }
            }
            launch {
                val histRes = currentClient.history()
                histRes.onSuccess { items ->
                    _state.update { it.copy(history = items.take(200)) }
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

    /** [fingerprint]: the fingerprint of the prompt being cancelled, when one is shown (see [RelayClient.cancel]). */
    suspend fun cancel(paneId: String, expectedSeq: Long, fingerprint: String? = null): Result<Unit> {
        val currentClient = getClient() ?: return notPaired()
        return refreshAfter(currentClient.cancel(paneId, expectedSeq, fingerprint))
    }

    suspend fun prompt(paneId: String, text: String, expectedSeq: Long): Result<Unit> {
        val currentClient = getClient() ?: return notPaired()
        return refreshAfter(currentClient.prompt(paneId, text, expectedSeq))
    }
}
