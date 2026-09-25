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
    val history: List<HistoryItem> = emptyList()
)
