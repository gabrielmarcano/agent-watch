package com.gabriel.agentwatch.data

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import com.gabriel.agentwatch.model.HostEvent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AgentStoreTest {

    private fun agent(pane: String, seq: Long, status: String = "working", label: String = pane) =
        AgentState(pane_id = pane, label = label, status = status, state_change_seq = seq)

    private fun snapshot(vararg agents: AgentState, host: Boolean = true, herdr: Boolean = true) =
        AgentsSnapshot(host_online = host, herdr_online = herdr, agents = agents.toList())

    private fun AgentStore.seqs(): Map<String, Long> = agents().associate { it.pane_id to it.state_change_seq }

    @Test
    fun refreshOlderThanAnAppliedSseEventKeepsTheSseState() {
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 5)))
        val since = store.beginFetch()
        store.applyAgent(agent("A", 6, status = "blocked"))

        store.applyFetched(snapshot(agent("A", 5)), since)

        assertEquals("blocked", store.agents().single().status)
        assertEquals(6L, store.agents().single().state_change_seq)
    }

    @Test
    fun refreshNewerThanLocalStateWins() {
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 5)))
        val since = store.beginFetch()

        store.applyFetched(snapshot(agent("A", 7, status = "done")), since)

        assertEquals(mapOf("A" to 7L), store.seqs())
        assertEquals("done", store.agents().single().status)
    }

    @Test
    fun equalSeqRefreshWithNoSseInBetweenTakesTheFetchedCopy() {
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 5, label = "old")))
        val since = store.beginFetch()

        store.applyFetched(snapshot(agent("A", 5, label = "new")), since)

        assertEquals("new", store.agents().single().label)
    }

    @Test
    fun sseEventOlderThanARefreshedStateIsIgnoredButEqualSeqApplies() {
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 5)))
        store.applyFetched(snapshot(agent("A", 7, status = "blocked")), store.beginFetch())

        assertFalse(store.applyAgent(agent("A", 6, status = "working")))
        assertEquals("blocked", store.agents().single().status)

        assertTrue(store.applyAgent(agent("A", 7, status = "blocked", label = "renamed")))
        assertEquals("renamed", store.agents().single().label)
    }

    @Test
    fun sseStreamOrderWinsForPanesItOwnsEvenIfTheSeqGoesDown() {
        // herdr restarted: its seq counter restarted too. Only a refresh-sourced state is seq-guarded.
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 500)))

        assertTrue(store.applyAgent(agent("A", 3, status = "idle")))

        assertEquals(mapOf("A" to 3L), store.seqs())
    }

    @Test
    fun paneAddedBySseDuringARefreshSurvivesIt() {
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 1)))
        val since = store.beginFetch()
        store.applyAgent(agent("B", 1))

        store.applyFetched(snapshot(agent("A", 1)), since)

        assertEquals(setOf("A", "B"), store.seqs().keys)
    }

    @Test
    fun paneRemovedBySseDuringARefreshStaysRemoved() {
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 1), agent("B", 1)))
        val since = store.beginFetch()
        store.applyRemoved("B")

        store.applyFetched(snapshot(agent("A", 1), agent("B", 1)), since)

        assertEquals(setOf("A"), store.seqs().keys)
    }

    @Test
    fun paneMissingFromARefreshIsDroppedWhenSseSaidNothingNewer() {
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 1), agent("B", 1)))
        store.applyAgent(agent("B", 2)) // before the fetch starts
        val since = store.beginFetch()

        store.applyFetched(snapshot(agent("A", 1)), since)

        assertEquals(setOf("A"), store.seqs().keys)
    }

    @Test
    fun newPaneInARefreshIsAdded() {
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 1)))
        val since = store.beginFetch()

        store.applyFetched(snapshot(agent("A", 1), agent("C", 4)), since)

        assertEquals(setOf("A", "C"), store.seqs().keys)
    }

    @Test
    fun sseSnapshotDuringARefreshBeatsTheOlderFetch() {
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 5)))
        val since = store.beginFetch()
        store.applySnapshot(snapshot(agent("A", 6), agent("D", 1)))

        store.applyFetched(snapshot(agent("A", 5), agent("C", 1)), since)

        assertEquals(mapOf("A" to 6L, "D" to 1L), store.seqs())
    }

    @Test
    fun hostFlagsFromAnOlderRefreshDoNotOverrideAnSseHostEvent() {
        val store = AgentStore()
        store.applySnapshot(snapshot(host = true, herdr = true))
        val since = store.beginFetch()
        store.applyHost(HostEvent(host_online = false, herdr_online = false))

        store.applyFetched(snapshot(host = true, herdr = true), since)

        assertFalse(store.hostOnline)
        assertFalse(store.herdrOnline)
    }

    @Test
    fun hostFlagsFromARefreshApplyWhenSseSaidNothingNewer() {
        val store = AgentStore()
        store.applySnapshot(snapshot(host = true, herdr = true))
        val since = store.beginFetch()

        store.applyFetched(snapshot(host = true, herdr = false), since)

        assertTrue(store.hostOnline)
        assertFalse(store.herdrOnline)
    }

    @Test
    fun agentsAreSortedBySeverityThenLabel() {
        val store = AgentStore()
        store.applySnapshot(
            snapshot(
                agent("1", 1, "idle", "b"),
                agent("2", 1, "blocked", "z"),
                agent("3", 1, "idle", "A"),
                agent("4", 1, "done", "m")
            )
        )

        assertEquals(listOf("2", "4", "3", "1"), store.agents().map { it.pane_id })
    }

    @Test
    fun clearForgetsEverything() {
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 1)))

        store.clear()

        assertTrue(store.agents().isEmpty())
        assertFalse(store.hostOnline)
    }
}
