package com.gabriel.agentwatch.complication

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.IOException

class ComplicationContentTest {

    private fun agents(vararg statuses: String) = statuses.mapIndexed { i, s -> AgentState(pane_id = "p$i", status = s) }
    private fun loaded(vararg statuses: String, host: Boolean = true) =
        Result.success(AgentsSnapshot(host_online = host, herdr_online = true, agents = agents(*statuses)))

    @Test
    fun unpairedOfflineAndErrorAreDistinct() {
        assertEquals(ComplicationContent(ComplicationKind.NOT_PAIRED, 0), complicationContent(paired = false, result = null))
        assertEquals(ComplicationContent(ComplicationKind.UNREACHABLE, 0), complicationContent(true, Result.failure(IOException())))
        assertEquals(ComplicationContent(ComplicationKind.MAC_OFFLINE, 0), complicationContent(true, loaded("blocked", host = false)))
    }

    @Test
    fun theMostUrgentStatusWinsWithItsCount() {
        assertEquals(ComplicationContent(ComplicationKind.NEEDS_YOU, 2), complicationContent(true, loaded("idle", "blocked", "done", "blocked")))
        assertEquals(ComplicationContent(ComplicationKind.DONE, 1), complicationContent(true, loaded("idle", "working", "done")))
        assertEquals(ComplicationContent(ComplicationKind.WORKING, 2), complicationContent(true, loaded("working", "idle", "working")))
    }

    @Test
    fun onlyIdleOrUnknownAgentsAreCountedAsIdle() {
        assertEquals(ComplicationContent(ComplicationKind.IDLE, 3), complicationContent(true, loaded("idle", "unknown", "idle")))
    }

    @Test
    fun noAgents() {
        assertEquals(ComplicationContent(ComplicationKind.NO_AGENTS, 0), complicationContent(true, loaded()))
    }
}
