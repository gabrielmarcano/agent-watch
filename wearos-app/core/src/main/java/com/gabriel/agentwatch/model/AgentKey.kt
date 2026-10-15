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
 * still exists, else the rule of [resolveTargetAgent] (latest `done`, else herdr's focused pane). Every
 * herdr has its own focused pane, so while more than one of [hosts] is online the focused fallback is
 * skipped: no target rather than an arbitrary one.
 */
fun resolveTarget(agents: List<AgentState>, pinned: AgentKey?, hosts: List<HostInfo>): AgentState? {
    if (pinned != null && pinned.paneId.isNotEmpty()) agents.findAgent(pinned)?.let { return it }
    return resolveTargetAgent(agents, null, useFocused = hosts.count { it.online } <= 1)
}

/**
 * Whether the [pinned] target is [agent], resolved like the tile resolves it ([findAgent]): a pin saved
 * before hosts (no host) shows as pinned while only one host has its pane.
 */
fun isPinnedTarget(pinned: AgentKey?, agent: AgentKey, agents: List<AgentState>): Boolean =
    pinned != null && (pinned == agent || agents.findAgent(pinned)?.key == agent)

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

/**
 * The agents a target (tile, Quick Dictate) may come from: those of online hosts, since an offline
 * host's last known agents cannot be answered (ARCHITECTURE.md §3a). While every host is offline, all
 * of them, so the tile still names its target and says the device is offline.
 */
fun targetCandidates(agents: List<AgentState>, hosts: List<HostInfo>, hostOnline: Boolean): List<AgentState> {
    if (hosts.isEmpty() || allHostsOffline(hosts, hostOnline)) return agents
    val online = hosts.filter { it.online }.mapTo(HashSet()) { it.id }
    return agents.filter { it.host in online }
}

fun AgentsSnapshot.targetCandidates(): List<AgentState> = targetCandidates(agents, hosts.orEmpty(), host_online)
