package com.gabriel.agentwatch.complication

import com.gabriel.agentwatch.model.AgentsSnapshot

/** What the status complication says, most urgent first. */
enum class ComplicationKind { NOT_PAIRED, UNREACHABLE, DEVICE_OFFLINE, NEEDS_YOU, DONE, WORKING, IDLE, NO_AGENTS }

/**
 * [count]: agents in [kind] (0 where it does not apply). [paneId], [label] and [agent] name the agent a
 * tap opens: the first blocked one, or the latest to finish; null opens the list.
 */
data class ComplicationContent(
    val kind: ComplicationKind,
    val count: Int,
    val paneId: String? = null,
    val label: String? = null,
    val agent: String? = null
)

/** Maps one `GET /v1/agents` ([result], null when not fetched) to the complication (severity: blocked > done > working > idle). */
fun complicationContent(paired: Boolean, result: Result<AgentsSnapshot>?): ComplicationContent {
    if (!paired) return ComplicationContent(ComplicationKind.NOT_PAIRED, 0)
    val snapshot = result?.getOrNull() ?: return ComplicationContent(ComplicationKind.UNREACHABLE, 0)
    if (!snapshot.host_online) return ComplicationContent(ComplicationKind.DEVICE_OFFLINE, 0)
    val agents = snapshot.agents
    val blocked = agents.filter { it.status == "blocked" }
    val done = agents.filter { it.status == "done" }
    val working = agents.count { it.status == "working" }
    return when {
        blocked.isNotEmpty() -> blocked.first().let {
            ComplicationContent(ComplicationKind.NEEDS_YOU, blocked.size, it.pane_id, it.label, it.agent)
        }
        done.isNotEmpty() -> done.maxBy { it.updated_at }.let {
            ComplicationContent(ComplicationKind.DONE, done.size, it.pane_id, it.label, it.agent)
        }
        working > 0 -> ComplicationContent(ComplicationKind.WORKING, working)
        agents.isNotEmpty() -> ComplicationContent(ComplicationKind.IDLE, agents.size)
        else -> ComplicationContent(ComplicationKind.NO_AGENTS, 0)
    }
}
