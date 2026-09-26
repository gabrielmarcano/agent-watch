package com.gabriel.agentwatch.tile

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import com.gabriel.agentwatch.model.resolveTargetAgent

/** What the Quick Dictate tile shows after its one `GET /v1/agents`. */
sealed interface TileContent {
    data object NotPaired : TileContent
    data object Unreachable : TileContent
    data class NoTarget(val needingYou: Int) : TileContent
    /** [othersNeedingYou]: blocked agents other than [agent], so the tile can point at them. */
    data class Target(val agent: AgentState, val othersNeedingYou: Int, val macOnline: Boolean) : TileContent
}

/** The target rule (pinned → latest `done` → focused) on the fetched [snapshot]; null means no fetch. */
fun tileContent(paired: Boolean, snapshot: Result<AgentsSnapshot>?, pinnedPaneId: String?): TileContent {
    if (!paired) return TileContent.NotPaired
    val agents = snapshot?.getOrNull() ?: return TileContent.Unreachable
    val blocked = agents.agents.filter { it.status == "blocked" }
    val target = resolveTargetAgent(agents.agents, pinnedPaneId) ?: return TileContent.NoTarget(blocked.size)
    return TileContent.Target(
        agent = target,
        othersNeedingYou = blocked.count { it.pane_id != target.pane_id },
        macOnline = agents.host_online
    )
}
