package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Before
import org.junit.Test

/** Item 2: re-pairing must restart SSE with the new relay URL and token. */
class RelayEngineRestartTest {
    private lateinit var oldRelay: FakeRelay
    private lateinit var newRelay: FakeRelay
    private lateinit var credentials: FakeCredentials
    private lateinit var engine: RelayEngine
    private val state = MutableStateFlow(UiState())

    private fun agent(pane: String) = AgentState(pane_id = pane, label = pane, status = "idle", state_change_seq = 1)

    @Before
    fun setUp() {
        oldRelay = FakeRelay().apply { agents = AgentsSnapshot(true, true, listOf(agent("OLD"))) }
        newRelay = FakeRelay().apply { agents = AgentsSnapshot(true, true, listOf(agent("NEW"))) }
        credentials = FakeCredentials(oldRelay.url, "old-token")
        engine = RelayEngine(state, credentials, reconnectDelayMs = { 50 })
        engine.start()
        awaitValue(state, what = "old relay live") { st -> st.agents.any { it.pane_id == "OLD" } }
    }

    @After
    fun tearDown() {
        engine.stop()
        oldRelay.close()
        newRelay.close()
    }

    /** What PairingScreen does after a successful pair: Prefs updated, then resetClient() + start(). */
    private fun pairWithNewRelay() {
        credentials.relayUrl = newRelay.url
        credentials.deviceToken = "new-token"
    }

    private fun assertOnNewRelayOnly() {
        val s = awaitValue(state, what = "new relay live") { st ->
            st.connection == Connection.Live && st.agents.map { it.pane_id } == listOf("NEW")
        }
        assertEquals("Bearer new-token", newRelay.eventsRequests().last().authorization)

        // The superseded stream must not leak into the new state.
        oldRelay.sendAgent(agent("GHOST"))
        Thread.sleep(200)
        assertFalse(state.value.agents.any { it.pane_id == "GHOST" })
        assertEquals(listOf("NEW"), s.agents.map { it.pane_id })
    }

    @Test
    fun legacyResetClientThenStartReconnectsWithTheNewCredentials() {
        pairWithNewRelay()

        engine.resetClient()
        engine.start()

        assertOnNewRelayOnly()
    }

    @Test
    fun restartReconnectsWithTheNewCredentials() {
        pairWithNewRelay()

        engine.restart()

        assertOnNewRelayOnly()
    }

    @Test
    fun restartDropsTheOldPairingsDataRightAway() {
        pairWithNewRelay()
        newRelay.close() // the new relay is unreachable: nothing may replace the old list

        engine.restart()

        val s = awaitValue(state, what = "old data cleared") { it.agents.isEmpty() }
        assertFalse(s.connection == Connection.Live)
    }
}
