package com.gabriel.agentwatch.tile

import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import com.gabriel.agentwatch.model.hostIsOnline
import com.gabriel.agentwatch.model.hostNameFor
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.model.resolveTarget

/** What the Quick Dictate tile shows after its one `GET /v1/agents`. */
sealed interface TileContent {
    data object NotPaired : TileContent
    data object Unreachable : TileContent
    data class NoTarget(val needingYou: Int) : TileContent
    /**
     * [othersNeedingYou]: blocked agents other than [agent], so the tile can point at them.
     * [macOnline]: whether [agent]'s host is online. [hostName]: its host's name, only when the relay
     * knows several hosts.
     */
    data class Target(
        val agent: AgentState,
        val othersNeedingYou: Int,
        val macOnline: Boolean,
        val hostName: String? = null
    ) : TileContent
}

/** The target rule (pinned → latest `done` → focused) on the fetched [snapshot], across every host; null means no fetch. */
fun tileContent(paired: Boolean, snapshot: Result<AgentsSnapshot>?, pinned: AgentKey?): TileContent {
    if (!paired) return TileContent.NotPaired
    val agents = snapshot?.getOrNull() ?: return TileContent.Unreachable
    val blocked = agents.agents.filter { it.status == "blocked" }
    val target = resolveTarget(agents.agents, pinned) ?: return TileContent.NoTarget(blocked.size)
    return TileContent.Target(
        agent = target,
        othersNeedingYou = blocked.count { it.key != target.key },
        macOnline = hostIsOnline(agents.hosts.orEmpty(), agents.host_online, target.host),
        hostName = hostNameFor(agents.hosts.orEmpty(), target.host)
    )
}
