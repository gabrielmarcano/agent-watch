package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem

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
    val history: List<HistoryItem> = emptyList(),
    /**
     * True while [agents] and the host flags are not backed by a live stream: before the first SSE
     * `snapshot`, after the stream closed, failed or went silent (no keepalive for 45 s) while it
     * reconnects, and after `stop()`. Cleared by the next `snapshot`. The list stays visible; the UI
     * should dim it or show "Reconnecting…" rather than present it as current.
     */
    val stale: Boolean = true
)
