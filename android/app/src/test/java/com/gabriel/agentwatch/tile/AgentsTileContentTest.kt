package com.gabriel.agentwatch.tile

import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.IOException

class AgentsTileContentTest {

    private fun agent(pane: String, status: String, updated: String = "2026-09-25T18:00:00Z") =
        AgentState(pane_id = pane, label = pane, status = status, updated_at = updated)

    private fun snapshot(vararg agents: AgentState, host: Boolean = true) =
        Result.success(AgentsSnapshot(host_online = host, herdr_online = true, agents = agents.toList()))

    @Test
    fun problemsComeFirst() {
        assertEquals(AgentsTile.NotPaired, agentsTile(paired = false, snapshot = null))
        assertEquals(AgentsTile.Unreachable, agentsTile(true, Result.failure(IOException())))
        assertEquals(AgentsTile.DeviceOffline, agentsTile(true, snapshot(agent("a", "blocked"), host = false)))
    }

    @Test
    fun theMostUrgentAgentsAreShownAndTheRestCounted() {
        val tile = agentsTile(
            true,
            snapshot(
                agent("idle", "idle"),
                agent("done-old", "done", "2026-09-25T18:00:00Z"),
                agent("working", "working"),
                agent("blocked", "blocked"),
                agent("done-new", "done", "2026-09-25T18:30:00Z"),
            ),
            max = 3
        )
        // Blocked, then done (latest first), then working, then idle.
        val expected = listOf(
            agent("blocked", "blocked"),
            agent("done-new", "done", "2026-09-25T18:30:00Z"),
            agent("done-old", "done", "2026-09-25T18:00:00Z"),
        )
        assertEquals(AgentsTile.Agents(expected, more = 2), tile)
    }

    @Test
    fun noAgents() {
        assertEquals(AgentsTile.Agents(emptyList(), more = 0), agentsTile(true, snapshot()))
    }

    // ---- phase 8: across hosts

    private val box = HostInfo(id = "box", name = "Box", online = true, herdr_online = true)
    private val mac = HostInfo(id = "main", name = "Mac", online = true, herdr_online = true)

    @Test
    fun theMostUrgentAcrossHostsNameTheirHost() {
        val tile = agentsTile(true, Result.success(AgentsSnapshot(
            host_online = true, herdr_online = true, hosts = listOf(box, mac),
            agents = listOf(agent("w1:p1", "idle").copy(host = "main"), agent("w1:p1", "blocked").copy(host = "box"), agent("w1:p2", "done").copy(host = "main"))
        )))
        tile as AgentsTile.Agents
        assertEquals(listOf("box", "main"), tile.shown.map { it.host })
        assertEquals(listOf("blocked", "done"), tile.shown.map { it.status })
        assertEquals(mapOf("box" to "Box", "main" to "Mac"), tile.hostNames)
        assertEquals(1, tile.more)
    }

    @Test
    fun oneHostNamesNothingAndOfflineHostsAreLeftOut() {
        val one = agentsTile(true, Result.success(AgentsSnapshot(host_online = true, hosts = listOf(mac), agents = listOf(agent("a", "done").copy(host = "main")))))
        assertEquals(emptyMap<String, String>(), (one as AgentsTile.Agents).hostNames)

        val macDown = agentsTile(true, Result.success(AgentsSnapshot(
            host_online = true, hosts = listOf(box, mac.copy(online = false)),
            agents = listOf(agent("a", "blocked").copy(host = "main"), agent("b", "idle").copy(host = "box"))
        )))
        assertEquals(listOf("b"), (macDown as AgentsTile.Agents).shown.map { it.pane_id })

        val allDown = agentsTile(true, Result.success(AgentsSnapshot(host_online = false, hosts = listOf(box.copy(online = false), mac.copy(online = false)))))
        assertEquals(AgentsTile.DeviceOffline, allDown)
    }
}
