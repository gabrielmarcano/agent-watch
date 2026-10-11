package com.gabriel.agentwatch.tile

import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import com.gabriel.agentwatch.model.findAgent
import com.gabriel.agentwatch.model.resolveTarget
import com.gabriel.agentwatch.model.targetCandidates

/** Who Quick Dictate sends to. */
sealed interface DictationTarget {
    data class Found(val agent: AgentState) : DictationTarget
    /** The pane the tile showed is gone. */
    data object Closed : DictationTarget
    /** No agent matches the target rule. */
    data object None : DictationTarget
}

/**
 * The tile passes the agent it displayed ("To: A"), host and pane: that agent or nothing, never another
 * one (a tile drawn before hosts passes no host: `findAgent`). Opened without one, the target rule
 * applies (pinned → latest `done` → focused) on online hosts' agents, like the tile ([targetCandidates]).
 */
fun dictationTarget(snapshot: AgentsSnapshot, launch: AgentKey?, pinned: AgentKey?): DictationTarget {
    if (launch != null && launch.paneId.isNotBlank()) {
        return snapshot.agents.findAgent(launch)?.let { DictationTarget.Found(it) } ?: DictationTarget.Closed
    }
    return resolveTarget(snapshot.targetCandidates(), pinned)?.let { DictationTarget.Found(it) } ?: DictationTarget.None
}
