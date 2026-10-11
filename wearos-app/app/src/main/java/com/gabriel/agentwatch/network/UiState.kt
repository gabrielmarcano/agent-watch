package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.model.HostInfo

sealed class Connection {
    object Connecting : Connection()
    object Live : Connection()
    data class Offline(val reason: String) : Connection()
}

/** Whether the watch holds a device token the relay accepts. */
enum class AuthState {
    /** A token is stored (assumed valid until the relay says otherwise). */
    PAIRED,
    /** No token stored: never paired, or cleared before this process started. */
    UNPAIRED,
    /** The relay answered 401 during this process; the token has been cleared. */
    REVOKED
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
    val stale: Boolean = true,
    val auth: AuthState = AuthState.PAIRED,
    /**
     * Every host the relay knows (contracts §1.6), in its order, each with its own flags; empty from a
     * relay that predates hosts. [hostOnline] and [herdrOnline] are then the aggregates (§1.5).
     */
    val hosts: List<HostInfo> = emptyList()
)
