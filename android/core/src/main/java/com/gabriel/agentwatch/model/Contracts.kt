package com.gabriel.agentwatch.model

import androidx.annotation.Keep

@Keep
data class PromptOption(
    val id: String = "",
    val label: String = "",
    val description: String? = null, // lines printed under the label (omitempty)
    val role: String = "choice" // "allow_once" | "allow_always" | "deny" | "choice"
)

@Keep
data class PendingPrompt(
    val kind: String = "unknown",          // "permission" | "question" | "unknown"
    val title: String = "",
    val detail: String? = null,
    val options: List<PromptOption> = emptyList(),
    val fingerprint: String = "",
    val raw_tail: String? = null
)

@Keep
data class AgentState(
    val pane_id: String = "",
    val agent: String = "",
    val label: String = "",
    val name: String? = null,
    val title: String? = null,
    val cwd: String? = null,
    val workspace_id: String = "",
    val workspace: String? = null,
    val status: String = "unknown",        // "idle" | "working" | "blocked" | "done" | "unknown"
    val focused: Boolean = false,
    val state_change_seq: Long = 0,
    val prompt: PendingPrompt? = null,
    val background_agents: Int = 0,        // agents the last turn left running while it waits on them; only with "working" (omitempty)
    val background_shells: Int = 0,        // shell commands still running in the background; any status (omitempty)
    val background_monitors: Int = 0,      // monitors still running in the background; any status (omitempty)
    val updated_at: String = "",
    // New fields go last: tests build these classes with positional arguments.
    val host: String = ""                  // relay-stamped host id; the key is (host, pane_id). "" from an older relay
)

@Keep
data class HistoryItem(
    val id: String = "",
    val pane_id: String = "",
    val agent: String = "",
    val label: String = "",
    val query: String? = null,
    val response: String = "",
    val source: String = "screen",         // "transcript" | "screen"
    val asked_at: String? = null,          // when the user sent it; omitted when unknown
    val completed_at: String = "",
    val host: String = ""
)

@Keep
data class AgentsSnapshot(
    val host_online: Boolean = false,      // at least one host connected
    val herdr_online: Boolean = false,     // every connected host reaches its herdr
    val agents: List<AgentState> = emptyList(),
    val generated_at: String = "",
    val hosts: List<HostInfo> = emptyList() // omitted by a relay that predates hosts
)

@Keep
data class HostInfo(
    val id: String = "",
    val name: String = "",
    val online: Boolean = false,
    val herdr_online: Boolean = false
)

@Keep
data class HostEvent(
    val host_online: Boolean = false,
    val herdr_online: Boolean = false,
    val hosts: List<HostInfo> = emptyList()
)

@Keep
data class PaneRef(
    val pane_id: String = "",
    val host: String = ""
)

@Keep
data class HistoryResponse(
    val items: List<HistoryItem> = emptyList()
)

@Keep
data class PairRequest(
    val code: String,
    val device_name: String
)

@Keep
data class PairResponse(
    val device_id: String = "",
    val device_token: String = ""
)

@Keep
data class PromptRequest(
    val text: String,
    val expected_seq: Long
)

@Keep
data class AnswerRequest(
    val option_id: String,
    val expected_seq: Long,
    val fingerprint: String
)

@Keep
data class CancelRequest(
    val expected_seq: Long,
    val fingerprint: String? = null
)

@Keep
data class PushRegisterRequest(
    val platform: String = "fcm",
    val token: String
)

@Keep
data class SessionOptionsResponse(
    val kinds: List<String> = emptyList(),
    val roots: List<String> = emptyList()
)

typealias SessionOptions = SessionOptionsResponse

@Keep
data class FolderEntry(
    val name: String = "",
    val path: String = ""
)

@Keep
data class FolderListResponse(
    val path: String = "",
    val parent: String? = null,
    val roots: List<String> = emptyList(),
    val entries: List<FolderEntry> = emptyList(),
    val truncated: Boolean = false
)

@Keep
data class SessionCreateRequest(
    val kind: String,
    val cwd: String,
    val label: String? = null
)

@Keep
data class SessionCreated(
    val host: String = "",
    val pane_id: String = "",
    val workspace_id: String = "",
    val tab_id: String = "",
    val kind: String = "",
    val label: String = ""
)

@Keep
data class UpdateInfo(
    val app: String = "",
    val version_code: Long = 0,
    val version_name: String = "",
    val sha256: String = "",
    val size: Long = 0
)

@Keep
data class ErrorBody(
    val code: String = "internal",
    val message: String = ""
)

@Keep
data class ErrorResponse(
    val error: ErrorBody = ErrorBody()
)

fun AgentState.severity(): Int = when (status) {
    "blocked" -> 4
    "done" -> 3
    "working" -> 2
    "idle" -> 1
    else -> 0
}

/**
 * Quick-dictation target (android/ARCHITECTURE.md §4b): the pinned agent if it still exists, else the most
 * recently finished (`done`) one, else herdr's focused pane (only with [useFocused]), else none. Never an
 * arbitrary agent: a dictated prompt must not land in a pane the user did not choose.
 */
fun resolveTargetAgent(agents: List<AgentState>, pinnedPaneId: String?, useFocused: Boolean = true): AgentState? {
    if (agents.isEmpty()) return null
    if (!pinnedPaneId.isNullOrEmpty()) {
        val pinned = agents.find { it.pane_id == pinnedPaneId }
        if (pinned != null) return pinned
    }
    val doneAgents = agents.filter { it.status == "done" }
    if (doneAgents.isNotEmpty()) {
        return doneAgents.maxByOrNull { it.updated_at }
    }
    return if (useFocused) agents.find { it.focused } else null
}
