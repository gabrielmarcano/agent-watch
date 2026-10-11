package com.gabriel.agentwatch.ui.logic

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.network.UiState

/**
 * One page of the agent list. [host] is null on the single page shown while the relay knows at most
 * one host (or predates hosts): that page holds every agent and looks as before hosts, with no host
 * name and no page indicator.
 */
data class HostPage(val host: HostInfo?, val agents: List<AgentState>) {
    /** The Compose key of the page: its host's id ("" for the single page). */
    val key: String get() = host?.id.orEmpty()
}

/**
 * The list's pages (ARCHITECTURE.md §4a): one per host in the relay's `hosts` order (contracts §1.5),
 * each with only that host's agents, in the store's order. With fewer than two hosts, one page with
 * every agent. An agent whose host is not listed shows on no page (the relay lists every host it has
 * agents of).
 */
fun hostPages(state: UiState): List<HostPage> {
    if (state.hosts.size < 2) return listOf(HostPage(null, state.agents))
    val byHost = state.agents.groupBy { it.host }
    return state.hosts.map { HostPage(it, byHost[it.id].orEmpty()) }
}

/** The page's notice: the relay link, then its own host's flags (every host's aggregate on the single page). */
fun pageStatus(state: UiState, page: HostPage, previous: ListNotice? = null): ListStatus {
    val host = page.host ?: return listStatus(state, previous)
    return listStatus(state, host.online, host.herdr_online, page.agents.isNotEmpty(), previous)
}
