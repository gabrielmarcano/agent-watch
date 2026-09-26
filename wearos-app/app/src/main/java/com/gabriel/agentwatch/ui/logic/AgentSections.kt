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
