package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/** Item 1: a half-open SSE socket (no keepalive for too long) must be detected, flagged stale and reconnected. */
class RelayEngineSilenceTest {
    private lateinit var relay: FakeRelay
    private lateinit var engine: RelayEngine
    private val state = MutableStateFlow(UiState())

    @Before
    fun setUp() {
        relay = FakeRelay().apply {
            agents = AgentsSnapshot(true, true, listOf(AgentState(pane_id = "A", label = "A", status = "idle", state_change_seq = 1)))
        }
        engine = RelayEngine(
            state,
            FakeCredentials(relay.url, "tok"),
            http = RelayHttpClients.create(sseSilenceTimeoutMs = 500),
            reconnectDelayMs = { 50 }
        )
    }

    @After
    fun tearDown() {
        engine.stop()
        relay.close()
    }

    private fun awaitFresh() = awaitValue(state, what = "live and fresh") {
        it.connection == Connection.Live && !it.stale && it.agents.isNotEmpty()
    }

    @Test
    fun theProductionSilenceTimeoutIsThreeMissedKeepalives() {
        // The relay writes a keepalive every 15 s (contracts §2.3).
        assertEquals(45_000, RelayHttpClients.shared.sse.readTimeoutMillis)
    }

    @Test
    fun aSilentStreamIsFlaggedStaleAndReconnected() {
        engine.start()
        awaitFresh()

        // The relay never writes again, like a socket left half-open by a Wi-Fi/Bluetooth handoff.
        val stale = awaitValue(state, timeoutMs = 3_000, what = "stale after silence") { it.stale }
        assertTrue("the old list stays visible while reconnecting", stale.agents.isNotEmpty())

        awaitTrue(timeoutMs = 3_000, what = "a second /v1/events") { relay.eventsRequests().size >= 2 }
        awaitFresh()
    }

    @Test
    fun keepalivesKeepTheStreamLive() {
        engine.start()
        awaitFresh()

        repeat(8) {
            Thread.sleep(200)
            relay.send(":\n\n")
        }

        assertEquals("1.6 s of keepalives with a 0.5 s timeout: no reconnect", 1, relay.eventsRequests().size)
        assertFalse(state.value.stale)
    }

    @Test
    fun staleUntilTheFirstSnapshotAndAfterStop() {
        assertTrue(state.value.stale)
        engine.start()
        awaitFresh()

        engine.stop()

        assertTrue(state.value.stale)
    }

    @Test
    fun aDroppedStreamIsStaleUntilTheNextSnapshot() {
        engine.start()
        awaitFresh()
        relay.eventsStatus = 503 // the relay is back but refuses streams for now

        relay.dropStreams()

        awaitValue(state, what = "stale after drop") { it.stale && it.connection is Connection.Offline }
        relay.eventsStatus = 200
        awaitFresh()
    }
}
