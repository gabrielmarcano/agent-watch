package com.gabriel.agentwatch.ui.logic

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.network.Connection
import com.gabriel.agentwatch.network.UiState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Phase 8: the list is one page per host; with fewer than two hosts it is the list as before hosts. */
class HostPagesTest {

    private val box = HostInfo(id = "box", name = "Box", online = true, herdr_online = true)
    private val mac = HostInfo(id = "main", name = "Mac", online = true, herdr_online = true)
    private val onMac = AgentState(pane_id = "w1:p1", host = "main", label = "api", status = "blocked")
    private val onBox = AgentState(pane_id = "w1:p1", host = "box", label = "api", status = "idle")
    private val alsoOnBox = AgentState(pane_id = "w1:p2", host = "box", label = "web", status = "working")

    private fun live(agents: List<AgentState>, hosts: List<HostInfo>) = UiState(
        connection = Connection.Live, hostOnline = true, herdrOnline = true, agents = agents, stale = false, hosts = hosts
    )

    @Test
    fun onePagePerHostInTheRelaysOrder() {
        val pages = hostPages(live(listOf(onMac, onBox, alsoOnBox), listOf(box, mac)))

        assertEquals(listOf("box", "main"), pages.map { it.key })
        assertEquals(listOf(onBox, alsoOnBox), pages[0].agents)
        assertEquals(listOf(onMac), pages[1].agents)
        assertEquals(box, pages[0].host)
    }

    @Test
    fun aHostWithoutAgentsStillHasItsPage() {
        val pages = hostPages(live(listOf(onMac), listOf(box, mac)))
        assertEquals(2, pages.size)
        assertTrue(pages[0].agents.isEmpty())
    }

    @Test
    fun withoutHostsTheListIsOnePageAsBefore() {
        val legacy = AgentState(pane_id = "w1:p1", label = "api", status = "idle")
        val state = live(listOf(legacy), emptyList())

        val pages = hostPages(state)

        assertEquals(1, pages.size)
        assertNull("no host name, no indicator", pages[0].host)
        assertEquals(listOf(legacy), pages[0].agents)
        assertEquals(listStatus(state), pageStatus(state, pages[0]))
    }

    @Test
    fun withOneHostTheListIsOnePageAsBefore() {
        val state = live(listOf(onMac), listOf(mac)).copy(hostOnline = false, herdrOnline = false)

        val pages = hostPages(state)

        assertEquals(1, pages.size)
        assertNull(pages[0].host)
        assertEquals(ListNotice.MAC_OFFLINE, pageStatus(state, pages[0]).notice)
    }

    @Test
    fun eachPageSaysItsOwnHostsState() {
        val state = live(listOf(onMac, onBox), listOf(box.copy(herdr_online = false), mac.copy(online = false, herdr_online = false)))
            // The aggregates (contracts §1.5): at least one online; not every online host reaches herdr.
            .copy(hostOnline = true, herdrOnline = false)
        val (boxPage, macPage) = hostPages(state)

        assertEquals(ListNotice.HERDR_STOPPED, pageStatus(state, boxPage).notice)
        assertEquals(ListNotice.MAC_OFFLINE, pageStatus(state, macPage).notice)
        assertTrue(pageStatus(state, macPage).dimmed)

        val healthy = live(listOf(onMac, onBox), listOf(box, mac))
        assertNull(pageStatus(healthy, hostPages(healthy)[0]).notice)
    }

    @Test
    fun theRelayLinkComesBeforeAnyHostsState() {
        val state = live(listOf(onMac), listOf(box, mac.copy(online = false))).copy(stale = true, connection = Connection.Offline("x"))
        hostPages(state).forEach { assertEquals(ListNotice.RELAY_UNREACHABLE, pageStatus(state, it).notice) }
    }

    @Test
    fun listKeysStayUniqueWhenTwoHostsShareAPaneId() {
        // The list's Compose key; the bare pane id crashed on duplicates.
        val agents = listOf(onMac, onBox, alsoOnBox)
        val keys = agents.map { it.key.token }
        assertEquals(keys.size, keys.toSet().size)
        assertFalse(keys.contains("w1:p1"))
    }
}
