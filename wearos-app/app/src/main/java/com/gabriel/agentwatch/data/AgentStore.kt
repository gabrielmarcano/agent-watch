package com.gabriel.agentwatch.data

import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import com.gabriel.agentwatch.model.HostEvent
import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.model.severity

/**
 * The watch's copy of the relay's agent list, fed by two sources that race each other:
 *
 * - **SSE** (`snapshot`, `agent`, `agent_removed`, `host`): ordered within one stream.
 * - **`GET /v1/agents`** (a refresh): its response can be older than SSE events applied while it was in flight.
 *
 * Every SSE mutation gets a mark from a counter. A refresh notes the counter when it starts
 * ([beginFetch]). When its response lands ([applyFetched]), it never overrides a pane that SSE touched
 * after that point, and within a pane the higher `state_change_seq` always wins.
 *
 * SSE `agent` events are only seq-guarded against a state that came from a refresh. Otherwise stream
 * order wins, so a herdr restart (whose seq counter restarts) cannot freeze a pane.
 *
 * Agents are keyed by (host, pane_id) ([AgentKey]): every herdr numbers its own panes.
 *
 * Not thread-safe: the owner serialises access.
 */
class AgentStore {
    private var agents = LinkedHashMap<AgentKey, AgentState>()
    /** Pane → mark of its last SSE mutation (update or removal). */
    private val marks = HashMap<AgentKey, Long>()
    /** Panes whose current state came from a refresh rather than the stream. */
    private val fromFetch = HashSet<AgentKey>()
    /** Mark of the last SSE snapshot: it vouches for every pane, listed or not. */
    private var snapshotMark = 0L
    private var hostMark = 0L
    private var counter = 0L

    var hostOnline = false
        private set
    var herdrOnline = false
        private set
    /** Every host the relay knows (contracts §1.6), in its order; empty from a relay that predates hosts. */
    var hosts: List<HostInfo> = emptyList()
        private set

    /** Sorted by severity (contracts §1.1), then label, like the relay's own list. */
    fun agents(): List<AgentState> = agents.values.sortedWith(AGENT_ORDER)

    /** SSE `snapshot`: replaces everything (contracts §2.3). */
    fun applySnapshot(snapshot: AgentsSnapshot) {
        counter++
        agents = LinkedHashMap(snapshot.agents.associateBy { it.key })
        marks.clear()
        fromFetch.clear()
        snapshotMark = counter
        hostMark = counter
        hostOnline = snapshot.host_online
        herdrOnline = snapshot.herdr_online
        hosts = snapshot.hosts.orEmpty()
    }

    /** SSE `agent`. Returns false when it was dropped as older than a refreshed state. */
    fun applyAgent(agent: AgentState): Boolean {
        val key = agent.key
        val current = agents[key]
        if (current != null && key in fromFetch && agent.state_change_seq < current.state_change_seq) {
            return false
        }
        counter++
        agents[key] = agent
        marks[key] = counter
        fromFetch.remove(key)
        return true
    }

    /** SSE `agent_removed`. Returns true if the pane was known. */
    fun applyRemoved(key: AgentKey): Boolean {
        counter++
        marks[key] = counter
        fromFetch.remove(key)
        return agents.remove(key) != null
    }

    /** SSE `host`. */
    fun applyHost(host: HostEvent) {
        counter++
        hostMark = counter
        hostOnline = host.host_online
        herdrOnline = host.herdr_online
        hosts = host.hosts.orEmpty()
    }

    /** Call before sending `GET /v1/agents`; pass the result to [applyFetched]. */
    fun beginFetch(): Long = counter

    /** Merges a `GET /v1/agents` response requested when [beginFetch] returned [since]. */
    fun applyFetched(snapshot: AgentsSnapshot, since: Long) {
        fun touchedSince(paneId: AgentKey) = maxOf(marks[paneId] ?: 0L, snapshotMark) > since

        val fetched = snapshot.agents.associateBy { it.key }
        val merged = LinkedHashMap<AgentKey, AgentState>()
        for ((paneId, current) in agents) {
            val incoming = fetched[paneId]
            val keepCurrent = when {
                incoming == null -> if (touchedSince(paneId)) true else continue // gone, and SSE said nothing newer
                incoming.state_change_seq > current.state_change_seq -> false
                incoming.state_change_seq < current.state_change_seq -> true
                else -> touchedSince(paneId)
            }
            if (keepCurrent) {
                merged[paneId] = current
            } else {
                merged[paneId] = incoming!!
                fromFetch.add(paneId)
            }
        }
        for ((paneId, incoming) in fetched) {
            if (paneId !in agents && !touchedSince(paneId)) {
                merged[paneId] = incoming
                fromFetch.add(paneId)
            }
        }
        fromFetch.retainAll(merged.keys)
        agents = merged

        if (maxOf(hostMark, snapshotMark) <= since) {
            hostOnline = snapshot.host_online
            herdrOnline = snapshot.herdr_online
            hosts = snapshot.hosts.orEmpty()
        }
    }

    /** Forgets everything (new pairing, revoked token). */
    fun clear() {
        counter++
        agents = LinkedHashMap()
        marks.clear()
        fromFetch.clear()
        snapshotMark = counter
        hostMark = counter
        hostOnline = false
        herdrOnline = false
        hosts = emptyList()
    }

    companion object {
        val AGENT_ORDER: Comparator<AgentState> =
            compareByDescending<AgentState> { it.severity() }.thenBy { it.label.lowercase() }
    }
}
