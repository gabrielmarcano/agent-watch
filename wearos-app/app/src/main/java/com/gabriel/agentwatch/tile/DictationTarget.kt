package com.gabriel.agentwatch.tile

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.resolveTargetAgent

/** Who Quick Dictate sends to. */
sealed interface DictationTarget {
    data class Found(val agent: AgentState) : DictationTarget
    /** The pane the tile showed is gone. */
    data object Closed : DictationTarget
    /** No agent matches the target rule. */
    data object None : DictationTarget
}

/**
 * The tile passes the `pane_id` it displayed ("To: A"): that pane or nothing, never another agent.
 * Opened without one, the target rule applies (pinned → latest `done` → focused).
 */
fun dictationTarget(agents: List<AgentState>, launchPaneId: String?, pinnedPaneId: String?): DictationTarget {
    if (!launchPaneId.isNullOrBlank()) {
        return agents.find { it.pane_id == launchPaneId }?.let { DictationTarget.Found(it) } ?: DictationTarget.Closed
    }
    return resolveTargetAgent(agents, pinnedPaneId)?.let { DictationTarget.Found(it) } ?: DictationTarget.None
}
