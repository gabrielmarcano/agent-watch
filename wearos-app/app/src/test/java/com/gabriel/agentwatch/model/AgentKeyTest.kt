package com.gabriel.agentwatch.model

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Phase 8: an agent is (host, pane_id); every herdr numbers its own panes. */
class AgentKeyTest {

    private val mac = HostInfo(id = "main", name = "Mac", online = true, herdr_online = true)
    private val box = HostInfo(id = "box", name = "Box", online = true, herdr_online = true)
    private val onMac = AgentState(pane_id = "w1:p1", host = "main", label = "api", status = "idle")
    private val onBox = AgentState(pane_id = "w1:p1", host = "box", label = "api", status = "idle")

    @Test
    fun theTokenIsThePaneAloneWithoutAHost() {
        assertEquals("w1:p1", AgentKey("", "w1:p1").token)
        assertEquals("box/w1:p1", AgentKey("box", "w1:p1").token)
        assertNotEquals(onMac.key.token, onBox.key.token)
    }

    @Test
    fun findAgentMatchesHostAndPane() {
        val agents = listOf(onMac, onBox)
        assertEquals(onBox, agents.findAgent(AgentKey("box", "w1:p1")))
        assertEquals(onMac, agents.findAgent(AgentKey("main", "w1:p1")))
        assertNull(agents.findAgent(AgentKey("other", "w1:p1")))
    }

    @Test
    fun aKeyWithoutAHostFindsItsPaneOnlyWhileOneHostHasIt() {
        // An intent or a pin saved before hosts.
        assertEquals(onBox, listOf(onBox).findAgent(AgentKey("", "w1:p1")))
        assertNull("never guess between two hosts", listOf(onMac, onBox).findAgent(AgentKey("", "w1:p1")))
        // An older relay: no host anywhere, exact match.
        val legacy = onMac.copy(host = "")
        assertEquals(legacy, listOf(legacy).findAgent(AgentKey("", "w1:p1")))
    }

    @Test
    fun thePinnedTargetIsTheOneOnItsHost() {
        val done = onMac.copy(status = "done", updated_at = "2026-10-10T10:00:00Z")
        val agents = listOf(done, onBox)
        assertEquals(onBox, resolveTarget(agents, AgentKey("box", "w1:p1")))
        assertEquals(done, resolveTarget(agents, AgentKey("main", "w1:p1")))
        assertEquals("a gone pin falls back to the rule", done, resolveTarget(agents, AgentKey("box", "w9:p9")))
        assertNull(resolveTarget(emptyList(), AgentKey("box", "w1:p1")))
    }

    @Test
    fun aHostIsNamedOnlyWhenThereAreSeveral() {
        assertNull(hostNameFor(emptyList(), "main"))
        assertNull(hostNameFor(listOf(mac), "main"))
        assertEquals("Mac", hostNameFor(listOf(mac, box), "main"))
        assertEquals("a host without a name shows its id", "box", hostNameFor(listOf(mac, box.copy(name = "")), "box"))
        assertNull(hostNameFor(listOf(mac, box), "gone"))
    }

    @Test
    fun offlineOnlyWhenEveryHostIs() {
        assertTrue("before hosts, the single flag", allHostsOffline(emptyList(), hostOnline = false))
        assertFalse(allHostsOffline(emptyList(), hostOnline = true))
        assertFalse(allHostsOffline(listOf(mac.copy(online = false), box), hostOnline = true))
        assertTrue(allHostsOffline(listOf(mac.copy(online = false), box.copy(online = false)), hostOnline = false))
    }

    @Test
    fun eachHostHasItsOwnOnlineFlag() {
        val hosts = listOf(mac.copy(online = false), box)
        assertFalse(hostIsOnline(hosts, hostOnline = true, host = "main"))
        assertTrue(hostIsOnline(hosts, hostOnline = true, host = "box"))
        assertTrue("before hosts, the single flag", hostIsOnline(emptyList(), hostOnline = true, host = ""))
    }

    @Test
    fun onlineAgentsLeaveOutOfflineHosts() {
        val snapshot = AgentsSnapshot(host_online = true, herdr_online = true, agents = listOf(onMac, onBox), hosts = listOf(mac.copy(online = false), box))
        assertEquals(listOf(onBox), snapshot.onlineAgents())
        val legacy = AgentsSnapshot(host_online = false, agents = listOf(onMac))
        assertTrue(legacy.onlineAgents().isEmpty())
        assertEquals(listOf(onMac), legacy.copy(host_online = true).onlineAgents())
    }
}
