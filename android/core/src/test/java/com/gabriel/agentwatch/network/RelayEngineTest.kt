package com.gabriel.agentwatch.network

import org.junit.Assert.assertTrue
import kotlinx.coroutines.runBlocking
import com.google.gson.Gson
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.HostEvent
import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.assertEquals
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
    fun aDroppedStreamIsReconnected() {
        engine.start()
        awaitValue(state, what = "live snapshot") { it.agents.isNotEmpty() }

        relay.dropStreams()

        awaitTrue(what = "second /v1/events") { relay.eventsRequests().size >= 2 }
        awaitValue(state, what = "live again") { it.connection == Connection.Live }
    }

    // ---- phase 8

    @Test
    fun theStreamKeepsTwoHostsApartAndCarriesTheirFlags() {
        val hosts = listOf(
            HostInfo(id = "box", name = "Box", online = true, herdr_online = true),
            HostInfo(id = "main", name = "Mac", online = true, herdr_online = true)
        )
        relay.agents = AgentsSnapshot(
            host_online = true, herdr_online = true, hosts = hosts,
            agents = listOf(agent("w1:p1", 1).copy(host = "main"), agent("w1:p1", 1).copy(host = "box"))
        )
        engine.start()
        awaitValue(state, what = "two hosts") { it.agents.size == 2 && it.hosts == hosts }

        relay.send("event: agent_removed\ndata: {\"host\":\"box\",\"pane_id\":\"w1:p1\"}\n\n")
        val s = awaitValue(state, what = "box's pane removed") { it.agents.size == 1 }
        assertEquals(listOf("main"), s.agents.map { it.host })

        val offline = hosts.map { if (it.id == "main") it.copy(online = false, herdr_online = false) else it }
        relay.send("event: host\ndata: ${Gson().toJson(HostEvent(host_online = true, herdr_online = true, hosts = offline))}\n\n")
        awaitValue(state, what = "main offline") { it.hosts == offline }
    }

    @Test
    fun commandsGoToTheAgentsHostThroughTheEngine() = runBlocking {
        engine.start()
        awaitValue(state, what = "live snapshot") { it.agents.isNotEmpty() }

        assertTrue(engine.answer(AgentKey("box", "w1:p1"), "opt-1", 3, "fp").isSuccess)
        assertTrue(engine.prompt(AgentKey("", "w1:p1"), "hi", 3).isSuccess)

        val paths = relay.requests.map { it.path }
        assertTrue(paths.toString(), "/v1/hosts/box/agents/w1%3Ap1/answer" in paths)
        assertTrue(paths.toString(), "/v1/agents/w1%3Ap1/prompt" in paths)
    }
}
