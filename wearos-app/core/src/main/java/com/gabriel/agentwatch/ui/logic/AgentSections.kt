package com.gabriel.agentwatch.ui.logic

import com.gabriel.agentwatch.model.AgentState

/** What an agent needs from the user, most urgent first. The list shows one section per value. */
enum class AttentionSection { NEEDS_YOU, DONE, WORKING, IDLE, UNKNOWN }

data class SectionedAgents(val section: AttentionSection, val agents: List<AgentState>)

fun AgentState.attentionSection(): AttentionSection = when (status) {
    "blocked" -> AttentionSection.NEEDS_YOU
    "done" -> AttentionSection.DONE
    "working" -> AttentionSection.WORKING
    "idle" -> AttentionSection.IDLE
    else -> AttentionSection.UNKNOWN
}

/**
 * The background agents this agent waits on after its turn ended (contracts.md §1.2): its reply is
 * ready although herdr still reports it working. 0 otherwise.
 */
fun AgentState.waitingOnBackground(): Int =
    if (status == "working" && background_agents > 0) background_agents else 0

/** What [AgentState] still runs in the background, by kind; all 0 when nothing does. */
data class BackgroundCounts(val agents: Int, val shells: Int, val monitors: Int) {
    val any: Boolean get() = agents > 0 || shells > 0 || monitors > 0
}

/**
 * The agents it waits on ([waitingOnBackground]) and the shells and monitors it still runs, which
 * hold with any status (contracts.md §1.2): they only say a report will come back to the agent.
 */
fun AgentState.backgroundCounts(): BackgroundCounts =
    BackgroundCounts(waitingOnBackground(), background_shells.coerceAtLeast(0), background_monitors.coerceAtLeast(0))

/** The status the UI draws: `done` while the agent only waits on background agents, else herdr's. Sections, sorting and surfaces keep herdr's. */
fun AgentState.shownStatus(): String = if (waitingOnBackground() > 0) "done" else status

/**
 * Groups [agents] by [AttentionSection], across workspaces, so a blocked agent never sits under idle
 * ones. Empty sections are left out. Inside a section the input order is kept (the store sorts by
 * label), so rows don't jump around as agents update.
 */
fun attentionSections(agents: List<AgentState>): List<SectionedAgents> {
    val bySection = agents.groupBy { it.attentionSection() }
    return AttentionSection.entries.mapNotNull { section ->
        bySection[section]?.let { SectionedAgents(section, it) }
    }
}
