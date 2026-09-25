package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/** Item 3: every 401 (stream, command, refresh) ends in one REVOKED state with the token cleared. */
class RelayEngineAuthTest {
    private lateinit var relay: FakeRelay
    private lateinit var credentials: FakeCredentials
    private lateinit var engine: RelayEngine
    private val state = MutableStateFlow(UiState())
    private var revokedHooks = 0

    private val unauthorized = 401 to """{"error":{"code":"unauthorized","message":"unknown token"}}"""

    @Before
    fun setUp() {
        relay = FakeRelay().apply {
            agents = AgentsSnapshot(true, true, listOf(AgentState(pane_id = "w1:p1", label = "A", status = "blocked", state_change_seq = 3)))
        }
        credentials = FakeCredentials(relay.url, "tok")
        engine = RelayEngine(
            state, credentials,
            reconnectDelayMs = { 50 },
            hooks = object : RelayEngineHooks {
                override fun onAuthRevoked() { revokedHooks++ }
            }
        )
    }

    @After
    fun tearDown() {
        engine.stop()
        relay.close()
    }

    private fun startLive() {
        engine.start()
        awaitValue(state, what = "live") { it.connection == Connection.Live && it.agents.isNotEmpty() }
    }

    private fun assertRevoked() {
        val s = awaitValue(state, what = "revoked") { it.auth == AuthState.REVOKED }
        assertNull("token cleared", credentials.deviceToken)
        assertTrue("no agents from a pairing we lost", s.agents.isEmpty())
        assertTrue(s.stale)
        assertEquals(false, s.hostOnline) // the UI must gate on auth, not show "Mac is offline"
        assertEquals(1, revokedHooks)
        assertNull(engine.getClient())

        // Nothing keeps hammering the relay with the dead token.
        val streams = relay.eventsRequests().size
        Thread.sleep(300)
        assertEquals(streams, relay.eventsRequests().size)
    }

    @Test
    fun a401OnACommandRevokesThePairing() = runBlocking {
        startLive()
        relay.responses["/v1/agents/w1%3Ap1/answer"] = unauthorized

        val err = engine.answer("w1:p1", "opt-1", 3, "fp").exceptionOrNull() as RelayError

        assertEquals(401, err.httpStatus)
        assertRevoked()
    }

    @Test
    fun a401OnRefreshRevokesThePairing() = runBlocking {
        startLive()
        relay.responses["/v1/agents"] = unauthorized

        engine.refresh()

        assertRevoked()
    }

    @Test
    fun a401OnTheStreamRevokesThePairing() {
        relay.eventsStatus = 401

        engine.start()

        assertRevoked()
    }

    @Test
    fun a401ForAnOldTokenDoesNotRevokeTheCurrentPairing() {
        startLive()
        credentials.deviceToken = "new-token" // re-paired while a request with the old token was in flight

        engine.onUnauthorized("tok")

        assertEquals(AuthState.PAIRED, state.value.auth)
        assertEquals("new-token", credentials.deviceToken)
        assertEquals(0, revokedHooks)
    }

    @Test
    fun revokedStaysRevokedAcrossStopStartUntilPairedAgain() {
        relay.eventsStatus = 401
        engine.start()
        awaitValue(state, what = "revoked") { it.auth == AuthState.REVOKED }

        engine.stop()
        engine.start()
        assertEquals(AuthState.REVOKED, state.value.auth)

        relay.eventsStatus = 200
        credentials.deviceToken = "fresh"
        engine.restart()
        awaitValue(state, what = "paired and live") { it.auth == AuthState.PAIRED && it.connection == Connection.Live }
    }

    @Test
    fun neverPairedIsUnpaired() {
        credentials.deviceToken = null

        engine.start()

        assertEquals(AuthState.UNPAIRED, state.value.auth)
    }
}
