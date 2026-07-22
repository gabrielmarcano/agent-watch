package com.gabriel.agentwatch.model

data class HistoryItem(
    val id: String = "",
    val query: String? = null,
    val response: String? = null,
    val timestamp: String? = null
)

data class AgentState(
    val status: String = "idle", // "idle" | "thinking" | "waiting_for_permission" | "done"
    val session_id: String? = null,
    val cwd: String? = null,
    val last_query: String? = null,
    val last_response: String? = null,
    val history: List<HistoryItem> = emptyList(),
    val tool_name: String? = null,
    val tool_input: Map<String, Any>? = null,
    val timestamp: String? = null
)
