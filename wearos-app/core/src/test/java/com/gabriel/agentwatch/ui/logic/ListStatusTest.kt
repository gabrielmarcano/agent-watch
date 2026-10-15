package com.gabriel.agentwatch.ui.logic

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.network.Connection
import com.gabriel.agentwatch.network.UiState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ListStatusTest {

    private val agents = listOf(AgentState(pane_id = "w1:p1", status = "idle"))
    private val live = UiState(
        connection = Connection.Live, hostOnline = true, herdrOnline = true, agents = agents, stale = false
    )

    @Test
    fun liveAndHealthyShowsNothing() {
        val status = listStatus(live)
        assertNull(status.notice)
        assertFalse(status.dimmed)
    }

    @Test
    fun beforeTheFirstSnapshotItIsConnectingNotMacOffline() {
        // Defaults before any snapshot: hostOnline=false, stale=true. The Mac is not known to be offline.
        val status = listStatus(UiState())
        assertEquals(ListNotice.CONNECTING, status.notice)
    }

    @Test
    fun aDeadStreamMeansTheRelayIsUnreachableAndTheListIsDimmed() {
        val status = listStatus(live.copy(connection = Connection.Offline("Failed to connect"), stale = true))
        assertEquals(ListNotice.RELAY_UNREACHABLE, status.notice)
        assertTrue(status.dimmed)
    }

    @Test
    fun reconnectingWithAKnownListDimsIt() {
        val status = listStatus(live.copy(connection = Connection.Connecting, stale = true))
        assertEquals(ListNotice.CONNECTING, status.notice)
        assertTrue(status.dimmed)
    }

    @Test
    fun macOfflineOnlyWhenTheRelaySaysSo() {
        val status = listStatus(live.copy(hostOnline = false))
        assertEquals(ListNotice.MAC_OFFLINE, status.notice)
        assertTrue("the relay keeps the last known list; it is not current", status.dimmed)
    }

    @Test
    fun herdrStopped() {
        val status = listStatus(live.copy(herdrOnline = false))
        assertEquals(ListNotice.HERDR_STOPPED, status.notice)
        assertTrue(status.dimmed)
    }

    @Test
    fun aRetryAfterAFailureKeepsSayingUnreachable() {
        // The engine goes Offline → Connecting → Offline on every retry; the notice must not flicker.
        val retrying = live.copy(connection = Connection.Connecting, stale = true)
        assertEquals(ListNotice.RELAY_UNREACHABLE, listStatus(retrying, previous = ListNotice.RELAY_UNREACHABLE).notice)
        assertNull(listStatus(live, previous = ListNotice.RELAY_UNREACHABLE).notice)
    }

    @Test
    fun theRelayProblemWinsOverStaleHostFlags() {
        val status = listStatus(live.copy(hostOnline = false, connection = Connection.Offline("x"), stale = true))
        assertEquals(ListNotice.RELAY_UNREACHABLE, status.notice)
    }
}
