package com.gabriel.agentwatch.data

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import com.gabriel.agentwatch.model.HostEvent
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
 * Not thread-safe: the owner serialises access.
 */
class AgentStore {
    private var agents = LinkedHashMap<String, AgentState>()
    /** Pane → mark of its last SSE mutation (update or removal). */
    private val marks = HashMap<String, Long>()
    /** Panes whose current state came from a refresh rather than the stream. */
    private val fromFetch = HashSet<String>()
    /** Mark of the last SSE snapshot: it vouches for every pane, listed or not. */
    private var snapshotMark = 0L
    private var hostMark = 0L
    private var counter = 0L

    var hostOnline = false
        private set
    var herdrOnline = false
        private set

    /** Sorted by severity (contracts §1.1), then label, like the relay's own list. */
    fun agents(): List<AgentState> = agents.values.sortedWith(AGENT_ORDER)

    /** SSE `snapshot`: replaces everything (contracts §2.3). */
    fun applySnapshot(snapshot: AgentsSnapshot) {
        counter++
        agents = LinkedHashMap(snapshot.agents.associateBy { it.pane_id })
        marks.clear()
        fromFetch.clear()
        snapshotMark = counter
        hostMark = counter
        hostOnline = snapshot.host_online
        herdrOnline = snapshot.herdr_online
    }

    /** SSE `agent`. Returns false when it was dropped as older than a refreshed state. */
    fun applyAgent(agent: AgentState): Boolean {
        val current = agents[agent.pane_id]
        if (current != null && agent.pane_id in fromFetch && agent.state_change_seq < current.state_change_seq) {
            return false
        }
        counter++
        agents[agent.pane_id] = agent
        marks[agent.pane_id] = counter
        fromFetch.remove(agent.pane_id)
        return true
    }

    /** SSE `agent_removed`. Returns true if the pane was known. */
    fun applyRemoved(paneId: String): Boolean {
        counter++
        marks[paneId] = counter
        fromFetch.remove(paneId)
        return agents.remove(paneId) != null
    }

    /** SSE `host`. */
    fun applyHost(host: HostEvent) {
        counter++
        hostMark = counter
        hostOnline = host.host_online
        herdrOnline = host.herdr_online
    }

    /** Call before sending `GET /v1/agents`; pass the result to [applyFetched]. */
    fun beginFetch(): Long = counter

    /** Merges a `GET /v1/agents` response requested when [beginFetch] returned [since]. */
    fun applyFetched(snapshot: AgentsSnapshot, since: Long) {
        fun touchedSince(paneId: String) = maxOf(marks[paneId] ?: 0L, snapshotMark) > since

        val fetched = snapshot.agents.associateBy { it.pane_id }
        val merged = LinkedHashMap<String, AgentState>()
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
    }

    companion object {
        val AGENT_ORDER: Comparator<AgentState> =
            compareByDescending<AgentState> { it.severity() }.thenBy { it.label.lowercase() }
    }
}
