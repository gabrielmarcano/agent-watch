package com.gabriel.agentwatch.tile

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import com.gabriel.agentwatch.model.allHostsOffline
import com.gabriel.agentwatch.model.hostNameFor
import com.gabriel.agentwatch.model.onlineAgents

/** What the Agents tile shows after its one `GET /v1/agents`. */
sealed interface AgentsTile {
    data object NotPaired : AgentsTile
    data object Unreachable : AgentsTile
    data object DeviceOffline : AgentsTile
    /**
     * [shown]: the most urgent agents, each a button; [more]: the agents left out. [hostNames]: each
     * shown agent's host name by host id, only when the relay knows several hosts (empty otherwise).
     */
    data class Agents(val shown: List<AgentState>, val more: Int, val hostNames: Map<String, String> = emptyMap()) : AgentsTile
}

/** How urgent an agent is for the tile: blocked, then done, working, idle, unknown. */
private fun AgentState.urgency(): Int = when (status) {
    "blocked" -> 0
    "done" -> 1
    "working" -> 2
    "idle" -> 3
    else -> 4
}

/**
 * The tile's agents: the [max] most urgent across every online host (blocked first, then the latest to
 * finish, then working and idle ones), and how many more there are. A problem (not paired, relay
 * unreachable, every host offline) replaces the list.
 */
fun agentsTile(paired: Boolean, snapshot: Result<AgentsSnapshot>?, max: Int = 2): AgentsTile {
    if (!paired) return AgentsTile.NotPaired
    val agents = snapshot?.getOrNull() ?: return AgentsTile.Unreachable
    val hosts = agents.hosts.orEmpty()
    if (allHostsOffline(hosts, agents.host_online)) return AgentsTile.DeviceOffline
    val ordered = agents.onlineAgents().sortedWith(
        compareBy<AgentState> { it.urgency() }
            .thenByDescending { if (it.status == "done") it.updated_at else "" }
            .thenBy { it.label.lowercase() }
    )
    val shown = ordered.take(max)
    val names = shown.mapNotNull { a -> hostNameFor(hosts, a.host)?.let { a.host to it } }.toMap()
    return AgentsTile.Agents(shown, (ordered.size - max).coerceAtLeast(0), names)
}
