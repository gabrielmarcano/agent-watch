package com.gabriel.agentwatch.complication

import com.gabriel.agentwatch.model.AgentsSnapshot
import com.gabriel.agentwatch.model.allHostsOffline
import com.gabriel.agentwatch.model.hostNameFor
import com.gabriel.agentwatch.model.onlineAgents

/** What the status complication says, most urgent first. */
enum class ComplicationKind { NOT_PAIRED, UNREACHABLE, DEVICE_OFFLINE, NEEDS_YOU, DONE, WORKING, IDLE, NO_AGENTS }

/**
 * [count]: agents in [kind] (0 where it does not apply). [paneId], [host], [label] and [agent] name the
 * agent a tap opens: the first blocked one, or the latest to finish; null opens the list. [hostName]:
 * that agent's host, only when the relay knows several hosts.
 */
data class ComplicationContent(
    val kind: ComplicationKind,
    val count: Int,
    val paneId: String? = null,
    val label: String? = null,
    val agent: String? = null,
    val host: String = "",
    val hostName: String? = null
)

/**
 * Maps one `GET /v1/agents` ([result], null when not fetched) to the complication (severity: blocked >
 * done > working > idle), across every online host. Device offline only when every host is.
 */
fun complicationContent(paired: Boolean, result: Result<AgentsSnapshot>?): ComplicationContent {
    if (!paired) return ComplicationContent(ComplicationKind.NOT_PAIRED, 0)
    val snapshot = result?.getOrNull() ?: return ComplicationContent(ComplicationKind.UNREACHABLE, 0)
    val hosts = snapshot.hosts.orEmpty()
    if (allHostsOffline(hosts, snapshot.host_online)) return ComplicationContent(ComplicationKind.DEVICE_OFFLINE, 0)
    val agents = snapshot.onlineAgents()
    val blocked = agents.filter { it.status == "blocked" }
    val done = agents.filter { it.status == "done" }
    val working = agents.count { it.status == "working" }
    return when {
        blocked.isNotEmpty() -> blocked.first().let {
            ComplicationContent(ComplicationKind.NEEDS_YOU, blocked.size, it.pane_id, it.label, it.agent, it.host, hostNameFor(hosts, it.host))
        }
        done.isNotEmpty() -> done.maxBy { it.updated_at }.let {
            ComplicationContent(ComplicationKind.DONE, done.size, it.pane_id, it.label, it.agent, it.host, hostNameFor(hosts, it.host))
        }
        working > 0 -> ComplicationContent(ComplicationKind.WORKING, working)
        agents.isNotEmpty() -> ComplicationContent(ComplicationKind.IDLE, agents.size)
        else -> ComplicationContent(ComplicationKind.NO_AGENTS, 0)
    }
}

/** The glyph a badge shows. */
enum class BadgeIcon { ALERT, AGENT, NOT_PAIRED, UNREACHABLE, DEVICE_OFFLINE }

/** Style B: a glyph and a number, no words. */
data class ComplicationBadge(val icon: BadgeIcon, val text: String)

/**
 * The short complication in style B: the alert glyph and how many agents need the user, else the
 * agent glyph and 0; a problem shows its own glyph and a dash.
 */
fun complicationBadge(content: ComplicationContent): ComplicationBadge = when (content.kind) {
    ComplicationKind.NEEDS_YOU -> ComplicationBadge(BadgeIcon.ALERT, "${content.count}")
    ComplicationKind.DONE, ComplicationKind.WORKING, ComplicationKind.IDLE, ComplicationKind.NO_AGENTS ->
        ComplicationBadge(BadgeIcon.AGENT, "0")
    ComplicationKind.NOT_PAIRED -> ComplicationBadge(BadgeIcon.NOT_PAIRED, "–")
    ComplicationKind.UNREACHABLE -> ComplicationBadge(BadgeIcon.UNREACHABLE, "–")
    ComplicationKind.DEVICE_OFFLINE -> ComplicationBadge(BadgeIcon.DEVICE_OFFLINE, "–")
}
