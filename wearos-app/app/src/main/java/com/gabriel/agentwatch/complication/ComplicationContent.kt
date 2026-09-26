package com.gabriel.agentwatch.complication

import com.gabriel.agentwatch.model.AgentsSnapshot

/** What the status complication says, most urgent first. */
enum class ComplicationKind { NOT_PAIRED, UNREACHABLE, MAC_OFFLINE, NEEDS_YOU, DONE, WORKING, IDLE, NO_AGENTS }

/** [count]: agents in [kind] (0 where it does not apply). */
data class ComplicationContent(val kind: ComplicationKind, val count: Int)

/** Maps one `GET /v1/agents` ([result], null when not fetched) to the complication (severity: blocked > done > working > idle). */
fun complicationContent(paired: Boolean, result: Result<AgentsSnapshot>?): ComplicationContent {
    if (!paired) return ComplicationContent(ComplicationKind.NOT_PAIRED, 0)
    val snapshot = result?.getOrNull() ?: return ComplicationContent(ComplicationKind.UNREACHABLE, 0)
    if (!snapshot.host_online) return ComplicationContent(ComplicationKind.MAC_OFFLINE, 0)
    val agents = snapshot.agents
    fun count(status: String) = agents.count { it.status == status }
    return when {
        count("blocked") > 0 -> ComplicationContent(ComplicationKind.NEEDS_YOU, count("blocked"))
        count("done") > 0 -> ComplicationContent(ComplicationKind.DONE, count("done"))
        count("working") > 0 -> ComplicationContent(ComplicationKind.WORKING, count("working"))
        agents.isNotEmpty() -> ComplicationContent(ComplicationKind.IDLE, agents.size)
        else -> ComplicationContent(ComplicationKind.NO_AGENTS, 0)
    }
}
