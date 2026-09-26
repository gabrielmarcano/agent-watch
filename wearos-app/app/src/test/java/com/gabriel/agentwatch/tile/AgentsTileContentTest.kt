package com.gabriel.agentwatch.tile

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
}
