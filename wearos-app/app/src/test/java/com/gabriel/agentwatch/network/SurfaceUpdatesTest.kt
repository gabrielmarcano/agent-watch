package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Item 11: ask the complication and tile to refresh when what they show changes, throttled. */
class SurfaceUpdatesTest {

    private fun agent(pane: String, status: String, label: String = pane, focused: Boolean = false) =
        AgentState(pane_id = pane, label = label, status = status, focused = focused)

    private val base = UiState(
        connection = Connection.Live,
        hostOnline = true,
        herdrOnline = true,
        agents = listOf(agent("A", "blocked"), agent("B", "working", focused = true)),
        stale = false
    )

    @Test
    fun theSignatureTracksWhatTheComplicationAndTileShow() {
        val sig = surfaceSignature(base, pinnedPaneId = null)

        assertEquals(SurfaceSignature(paired = true, hostOnline = true, blocked = 1, working = 1, agents = 2, targetLabel = "B"), sig)
    }

    @Test
    fun changesTheSurfacesDoNotShowKeepTheSameSignature() {
        val sig = surfaceSignature(base, null)
        val noisy = base.copy(
            agents = base.agents.map { it.copy(updated_at = "2026-09-25T10:00:00Z", state_change_seq = 99) },
            history = listOf(HistoryItem(id = "h1")),
            stale = true,
            connection = Connection.Connecting
        )

        assertEquals(sig, surfaceSignature(noisy, null))
    }

    @Test
    fun changesTheSurfacesShowChangeTheSignature() {
        val sig = surfaceSignature(base, null)

        assertNotEquals(sig, surfaceSignature(base.copy(hostOnline = false), null))
        assertNotEquals(sig, surfaceSignature(base.copy(agents = listOf(agent("A", "idle"), base.agents[1])), null))
        assertNotEquals("the tile label follows the pinned agent", sig, surfaceSignature(base, pinnedPaneId = "A"))
        assertNotEquals(sig, surfaceSignature(base.copy(auth = AuthState.REVOKED), null))
    }

    @Test
    fun theFirstChangeIsSentAtOnce() {
        val throttle = UpdateThrottle(minIntervalMs = 10_000)

        assertEquals(0L, throttle.onChange(now = 100_000))
    }

    @Test
    fun changesInsideTheWindowCoalesceIntoOneTrailingRequest() {
        val throttle = UpdateThrottle(minIntervalMs = 10_000)
        throttle.onChange(now = 100_000)

        assertEquals(9_000L, throttle.onChange(now = 101_000))
        assertNull("already scheduled", throttle.onChange(now = 102_000))
        assertNull(throttle.onChange(now = 105_000))

        throttle.onTrailingFired(now = 110_000)
        assertEquals("next window starts at the trailing request", 8_000L, throttle.onChange(now = 112_000))
    }

    @Test
    fun aChangeAfterTheWindowIsSentAtOnce() {
        val throttle = UpdateThrottle(minIntervalMs = 10_000)
        throttle.onChange(now = 100_000)

        assertEquals(0L, throttle.onChange(now = 110_001))
    }
}
