package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import kotlinx.coroutines.runBlocking
import org.junit.Before
import org.junit.Test

/** The engine's basic SSE → state path (regression net for the extraction out of RelayRepository). */
class RelayEngineTest {
    private lateinit var relay: FakeRelay
    private lateinit var engine: RelayEngine
    private val state = MutableStateFlow(UiState())

    private fun agent(pane: String, seq: Long, status: String = "working") =
        AgentState(pane_id = pane, label = pane, status = status, state_change_seq = seq)

    @Before
    fun setUp() {
        relay = FakeRelay()
        relay.agents = AgentsSnapshot(host_online = true, herdr_online = true, agents = listOf(agent("A", 1)))
        engine = RelayEngine(state, FakeCredentials(relay.url, "tok"), reconnectDelayMs = { 50 })
    }

    @After
    fun tearDown() {
        engine.stop()
        relay.close()
    }

    @Test
    fun startStreamsTheSnapshotWithTheBearerToken() {
        engine.start()

        val s = awaitValue(state, what = "live snapshot") { it.connection == Connection.Live && it.agents.isNotEmpty() }

        assertEquals(listOf("A"), s.agents.map { it.pane_id })
        assertEquals("Bearer tok", relay.eventsRequests().first().authorization)
    }

    @Test
    fun agentEventsUpdateTheList() {
        engine.start()
        awaitValue(state, what = "live snapshot") { it.agents.isNotEmpty() }

        relay.sendAgent(agent("B", 1, status = "blocked"))

        val s = awaitValue(state, what = "agent B") { st -> st.agents.any { it.pane_id == "B" } }
        assertEquals(listOf("B", "A"), s.agents.map { it.pane_id })
    }

    @Test
    fun agentNewsIsReportedForNotificationsAndSurfaces() {
        val updates = java.util.concurrent.CopyOnWriteArrayList<AgentsUpdate>()
        engine.stop()
        engine = RelayEngine(
            state, FakeCredentials(relay.url, "tok"), reconnectDelayMs = { 50 },
            hooks = object : RelayEngineHooks {
                override fun onAgentsUpdated(update: AgentsUpdate) { updates += update }
            }
        )
        engine.start()
        awaitTrue(what = "snapshot news") { updates.any { it is AgentsUpdate.All && it.agents.isNotEmpty() } }

        relay.sendAgent(agent("A", 2, status = "working"))
        relay.send("event: agent_removed\ndata: {\"pane_id\":\"A\"}\n\n")

        awaitTrue(what = "changed + removed news") {
            updates.any { it is AgentsUpdate.Changed && it.agent.state_change_seq == 2L } &&
                updates.any { it == AgentsUpdate.Removed("A") }
        }
    }

    @Test
    fun promptQueuedOfflineIsSentAfterFreshSnapshot() = runBlocking {
        val queue = FakePromptQueueStore()
        engine.stop()
        engine = RelayEngine(state, FakeCredentials(relay.url, "tok"), promptQueue = queue, reconnectDelayMs = { 50 })

        val queued = engine.prompt("A", "continue", 1)
        assertEquals("prompt_queued", (queued.exceptionOrNull() as RelayError).code)
        assertEquals(1, queue.queuedPrompts.size)

        engine.start()
        awaitValue(state, what = "queued prompt delivery") { it.connection == Connection.Live && relay.requests.any { r -> r.path == "/v1/agents/A/prompt" } }
        assertTrue(queue.queuedPrompts.isEmpty())
    }

    @Test
    fun queuedPromptStaysPendingWhenAgentSequenceChanged() = runBlocking {
        val queue = FakePromptQueueStore()
        engine.stop()
        engine = RelayEngine(state, FakeCredentials(relay.url, "tok"), promptQueue = queue, reconnectDelayMs = { 50 })
        val queued = engine.prompt("A", "continue", 1)
        assertEquals("prompt_queued", (queued.exceptionOrNull() as RelayError).code)

        relay.agents = AgentsSnapshot(host_online = true, herdr_online = true, agents = listOf(agent("A", 2)))
        engine.start()
        awaitValue(state, what = "fresh changed snapshot") { it.connection == Connection.Live && it.agents.first().state_change_seq == 2L }
        Thread.sleep(100)
        assertTrue(queue.queuedPrompts.isNotEmpty())
        assertTrue(relay.requests.none { it.path == "/v1/agents/A/prompt" })
    }

    @Test
    fun aDroppedStreamIsReconnected() {
        engine.start()
        awaitValue(state, what = "live snapshot") { it.agents.isNotEmpty() }

        relay.dropStreams()

        awaitTrue(what = "second /v1/events") { relay.eventsRequests().size >= 2 }
        awaitValue(state, what = "live again") { it.connection == Connection.Live }
    }
}
