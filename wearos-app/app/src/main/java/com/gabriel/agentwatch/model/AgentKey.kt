package com.gabriel.agentwatch.model

/**
 * An agent's identity: every herdr numbers its own panes, so `w1:p1` can exist on every host
 * (contracts §1.2). [host] is "" from a relay that predates hosts (one host).
 */
data class AgentKey(val host: String, val paneId: String) {
    /**
     * One string for this key, for maps, preferences and hashes: the pane id alone without a host (as
     * before hosts, so ids and stored values stay the same), else `host/pane`. Host ids never hold `/`
     * (contracts §1.6). Never parsed back: keep [host] and [paneId] apart wherever they travel.
     */
    val token: String get() = if (host.isEmpty()) paneId else "$host/$paneId"
}

val AgentState.key: AgentKey get() = AgentKey(host, pane_id)

val HistoryItem.key: AgentKey get() = AgentKey(host, pane_id)

/**
 * The agent [key] names. A key without a host (an intent or a pin saved before hosts, a tile drawn by
 * an older build) still finds its pane while only one host has it, like the relay's old command paths
 * (contracts §2.1); with several, it finds none rather than guess.
 */
fun List<AgentState>.findAgent(key: AgentKey): AgentState? {
    find { it.host == key.host && it.pane_id == key.paneId }?.let { return it }
    if (key.host.isNotEmpty()) return null
    return filter { it.pane_id == key.paneId }.singleOrNull()
}

/**
 * Quick-dictation target (wearos-app/ARCHITECTURE.md §4b) across every host: the [pinned] agent if it
 * still exists, else the rule of [resolveTargetAgent] (latest `done`, else herdr's focused pane).
 */
fun resolveTarget(agents: List<AgentState>, pinned: AgentKey?): AgentState? {
    if (pinned != null && pinned.paneId.isNotEmpty()) agents.findAgent(pinned)?.let { return it }
    return resolveTargetAgent(agents, null)
}

/**
 * The name to show for [host] next to an agent, or null while the relay knows fewer than two hosts:
 * with one machine nothing names it (ARCHITECTURE.md §4b).
 */
fun hostNameFor(hosts: List<HostInfo>, host: String): String? {
    if (hosts.size < 2) return null
    val info = hosts.find { it.id == host } ?: return null
    return info.name.ifBlank { info.id }
}

/**
 * Whether [host]'s bridge is connected: its own flag when the relay lists hosts, else the single
 * [hostOnline] flag of a relay that predates hosts.
 */
fun hostIsOnline(hosts: List<HostInfo>, hostOnline: Boolean, host: String): Boolean =
    if (hosts.isEmpty()) hostOnline else hosts.find { it.id == host }?.online ?: false

/** Every host is offline (no bridge connected). With no host list, the single flag decides. */
fun allHostsOffline(hosts: List<HostInfo>, hostOnline: Boolean): Boolean =
    if (hosts.isEmpty()) !hostOnline else hosts.none { it.online }

/** The agents of hosts that are online, for the surfaces: an offline host's last known agents cannot be answered. */
fun AgentsSnapshot.onlineAgents(): List<AgentState> {
    val known = hosts.orEmpty() // Gson leaves null for an explicit `null`
    if (known.isEmpty()) return if (host_online) agents else emptyList()
    val online = known.filter { it.online }.mapTo(HashSet()) { it.id }
    return agents.filter { it.host in online }
}
