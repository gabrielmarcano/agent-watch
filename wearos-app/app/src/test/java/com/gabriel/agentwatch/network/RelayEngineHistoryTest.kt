package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test

/** `history` events sent while the stream is down are never replayed: a reconnect re-fetches them. */
class RelayEngineHistoryTest {
    private lateinit var relay: FakeRelay
    private lateinit var engine: RelayEngine
    private val state = MutableStateFlow(UiState())

    private fun historyRequests() = relay.requests.count { it.path.substringBefore('?') == "/v1/history" }

    private fun item(id: String, at: String) =
        """{"id":"$id","pane_id":"A","agent":"claude","label":"A","response":"reply $id","source":"transcript","completed_at":"$at"}"""
    private val h1 = item("h1", "2026-10-05T10:00:00Z")
    private val h2 = item("h2", "2026-10-05T10:05:00Z")

    @Before
    fun setUp() {
        relay = FakeRelay()
        relay.agents = AgentsSnapshot(
            host_online = true, herdr_online = true,
            agents = listOf(AgentState(pane_id = "A", label = "A", status = "done", state_change_seq = 1))
        )
        relay.historyJson = """{"items":[$h1]}"""
        engine = RelayEngine(state, FakeCredentials(relay.url, "tok"), reconnectDelayMs = { 50 })
    }

    @After
    fun tearDown() {
        engine.stop()
        relay.close()
    }

    @Test
    fun aReconnectFetchesTheTurnsFinishedWhileTheStreamWasDown() {
        engine.start()
        awaitValue(state, what = "first history") { s -> s.history.any { it.id == "h1" } }
        awaitValue(state, what = "live") { it.connection == Connection.Live }

        // A turn finishes while the stream is down: its SSE event is lost.
        relay.historyJson = """{"items":[$h2,$h1]}"""
        relay.dropStreams()

        val s = awaitValue(state, what = "missed turn after reconnect") { st -> st.history.any { it.id == "h2" } }
        assertEquals(listOf("h2", "h1"), s.history.map { it.id })
    }

    // start() fetches on its own; its first snapshot must not fetch a second time.
    @Test
    fun theFirstSnapshotDoesNotFetchHistoryAgain() {
        engine.start()
        awaitValue(state, what = "live snapshot") { it.connection == Connection.Live && it.history.isNotEmpty() }
        Thread.sleep(200) // room for a duplicate fetch to show up

        assertEquals(1, historyRequests())

        relay.dropStreams()
        awaitTrue(what = "second /v1/events") { relay.eventsRequests().size >= 2 }
        awaitTrue(what = "history re-fetched once") { historyRequests() == 2 }
        Thread.sleep(200)

        assertEquals(2, historyRequests())
    }
}
