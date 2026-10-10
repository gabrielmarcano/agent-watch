package com.gabriel.agentwatch.data

import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import com.gabriel.agentwatch.model.HostEvent
import com.gabriel.agentwatch.model.HostInfo
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
        store.applyRemoved(AgentKey("", "B"))

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

    // ---- phase 8: two hosts that share a pane id

    private fun on(host: String, pane: String, seq: Long, status: String = "working") =
        AgentState(pane_id = pane, host = host, label = "$host-$pane", status = status, state_change_seq = seq)

    private val hosts = listOf(
        HostInfo(id = "box", name = "Box", online = true, herdr_online = true),
        HostInfo(id = "main", name = "Mac", online = true, herdr_online = false)
    )

    @Test
    fun twoHostsSharingAPaneIdAreTwoAgents() {
        val store = AgentStore()
        store.applySnapshot(AgentsSnapshot(host_online = true, herdr_online = false, agents = listOf(on("main", "w1:p1", 1), on("box", "w1:p1", 7)), hosts = hosts))

        assertEquals(2, store.agents().size)
        assertEquals(hosts, store.hosts)

        assertTrue(store.applyAgent(on("box", "w1:p1", 8, status = "blocked")))
        val byHost = store.agents().associateBy { it.host }
        assertEquals("blocked", byHost.getValue("box").status)
        assertEquals("the other host's pane is untouched", "working", byHost.getValue("main").status)

        assertTrue(store.applyRemoved(AgentKey("main", "w1:p1")))
        assertEquals(listOf("box"), store.agents().map { it.host })
        assertFalse("a removal without the host is another key", store.applyRemoved(AgentKey("", "w1:p1")))
        assertEquals(1, store.agents().size)
    }

    @Test
    fun aRefreshMergesEachHostsPaneOnItsOwn() {
        val store = AgentStore()
        store.applySnapshot(AgentsSnapshot(host_online = true, agents = listOf(on("main", "w1:p1", 5), on("box", "w1:p1", 5)), hosts = hosts))
        val since = store.beginFetch()
        store.applyAgent(on("box", "w1:p1", 6, status = "blocked"))

        store.applyFetched(AgentsSnapshot(host_online = true, agents = listOf(on("main", "w1:p1", 9, status = "done"), on("box", "w1:p1", 5)), hosts = hosts), since)

        val byHost = store.agents().associateBy { it.host }
        assertEquals("done", byHost.getValue("main").status)
        assertEquals("SSE kept the newer state of box's pane", "blocked", byHost.getValue("box").status)
    }

    @Test
    fun theHostEventCarriesEveryHostsFlags() {
        val store = AgentStore()
        store.applySnapshot(AgentsSnapshot(host_online = true, herdr_online = true, hosts = hosts))
        val offline = hosts.map { it.copy(online = it.id == "box", herdr_online = it.id == "box") }

        store.applyHost(HostEvent(host_online = true, herdr_online = true, hosts = offline))

        assertEquals(offline, store.hosts)
        store.clear()
        assertTrue(store.hosts.isEmpty())
    }

    @Test
    fun aRelayWithoutHostsLeavesTheListEmpty() {
        val store = AgentStore()
        store.applySnapshot(snapshot(agent("A", 1)))
        assertTrue(store.hosts.isEmpty())
        assertEquals(listOf(""), store.agents().map { it.host })
    }
}
