package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.approval.FeedbackSurface
import com.gabriel.agentwatch.approval.commandErrorFeedback
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.net.SocketTimeoutException

class RelayClientTest {
    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun baseUrl() = server.url("/").toString()

    private fun elapsedMs(startNanos: Long) = (System.nanoTime() - startNanos) / 1_000_000

    @Test
    fun cancellingTheCoroutineAbortsAHungCommandImmediately() = runBlocking {
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
        val client = RelayClient(baseUrl(), "tok")

        val start = System.nanoTime()
        val res = withTimeoutOrNull(300) { client.answer(AgentKey("", "w1:p1"), "opt-1", 5, "fp") }

        assertNull("the timeout must win", res)
        assertTrue("withTimeoutOrNull must not wait for OkHttp's read timeout (took ${elapsedMs(start)} ms)", elapsedMs(start) < 2_000)
    }

    @Test
    fun cancelledCallerGetsNoSwallowedFailure() = runBlocking {
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
        val client = RelayClient(baseUrl(), "tok")
        var returned: Result<*>? = null

        val start = System.nanoTime()
        val job = launch(Dispatchers.Default) { returned = client.agents() }
        delay(200)
        job.cancelAndJoin()

        assertNull("a cancelled request must throw CancellationException, not return a failure", returned)
        assertTrue("cancel must interrupt the HTTP call (took ${elapsedMs(start)} ms)", elapsedMs(start) < 2_000)
    }

    @Test
    fun commandCallTimeoutCapsTheWholeCallAndReadsAsRelayTimedOut() = runBlocking {
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
        val client = RelayClient(baseUrl(), "tok", RelayHttpClients.create(commandCallTimeoutMs = 300))

        val start = System.nanoTime()
        val err = client.prompt(AgentKey("", "w1:p1"), "hi", 1).exceptionOrNull()

        assertTrue("took ${elapsedMs(start)} ms", elapsedMs(start) < 2_000)
        assertTrue("callTimeout must surface as a SocketTimeoutException, got $err", err is SocketTimeoutException)
        assertEquals("Relay timed out", commandErrorFeedback(err!!, FeedbackSurface.NOTIFICATION).message)
    }

    @Test
    fun defaultCommandCallTimeoutFitsInsideGoAsync() {
        val callTimeout = RelayHttpClients.shared.command.callTimeoutMillis
        assertTrue("command callTimeout must be set and under 10 s, was $callTimeout", callTimeout in 1..9_000)
    }

    @Test
    fun everyClientSharesOnePoolAndDispatcher() {
        val a = RelayHttpClients.shared
        assertSame(a.rest.connectionPool, a.command.connectionPool)
        assertSame(a.rest.connectionPool, a.sse.connectionPool)
        assertSame(a.rest.dispatcher, a.command.dispatcher)
        assertSame(a.rest.dispatcher, a.sse.dispatcher)
    }

    @Test
    fun successfulCommandSendsBearerTokenAndEncodedPane() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"ok":true}"""))
        val client = RelayClient(baseUrl(), "tok")

        val res = client.answer(AgentKey("", "w5:pAE"), "opt-1", 334, "9f2c")

        assertTrue(res.isSuccess)
        val req = server.takeRequest()
        assertEquals("/v1/agents/w5%3ApAE/answer", req.path)
        assertEquals("Bearer tok", req.getHeader("Authorization"))
    }

    @Test
    fun promptSendsBearerTokenAndPromptBody() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"ok":true}"""))
        val client = RelayClient(baseUrl(), "tok")

        val res = client.prompt(AgentKey("", "w5:pAE"), "continue from the last step", 334)

        assertTrue(res.isSuccess)
        val req = server.takeRequest()
        assertEquals("POST", req.method)
        assertEquals("/v1/agents/w5%3ApAE/prompt", req.path)
        assertEquals("Bearer tok", req.getHeader("Authorization"))
        assertEquals(
            """{"text":"continue from the last step","expected_seq":334}""",
            req.body.readUtf8()
        )
    }

    @Test
    fun cancelSendsTheFingerprintWhenGiven() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"ok":true}"""))
        val client = RelayClient(baseUrl(), "tok")

        assertTrue(client.cancel(AgentKey("", "w1:p1"), 7, fingerprint = "9f2c61d0a4b3e871").isSuccess)

        val body = server.takeRequest().body.readUtf8()
        assertEquals("""{"expected_seq":7,"fingerprint":"9f2c61d0a4b3e871"}""", body)
    }

    @Test
    fun cancelWithoutFingerprintOmitsTheField() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"ok":true}"""))
        val client = RelayClient(baseUrl(), "tok")

        assertTrue(client.cancel(AgentKey("", "w1:p1"), 7).isSuccess)

        assertEquals("""{"expected_seq":7}""", server.takeRequest().body.readUtf8())
    }

    @Test
    fun a401IsReportedWithTheTokenThatWasRejected() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":{"code":"unauthorized","message":"no"}}"""))
        val rejected = mutableListOf<String>()
        val client = RelayClient(baseUrl(), "tok", onUnauthorized = { rejected += it })

        client.agents()

        assertEquals(listOf("tok"), rejected)
    }

    @Test
    fun clientsBuiltOutsideTheRepositoryReportThroughTheProcessWideListener() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(401))
        val rejected = mutableListOf<String>()
        RelayClient.unauthorizedListener = { rejected += it }
        try {
            RelayClient(baseUrl(), "tile-token").agents() // e.g. the tile or complication
        } finally {
            RelayClient.unauthorizedListener = null
        }

        assertEquals(listOf("tile-token"), rejected)
    }

    @Test
    fun a401WithoutATokenIsNotARevocation() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(401))
        val rejected = mutableListOf<String>()

        RelayClient(baseUrl(), null, onUnauthorized = { rejected += it }).pair("123456", "watch")

        assertTrue(rejected.isEmpty())
    }

    @Test
    fun relayErrorBodyIsMapped() = runBlocking {
        server.enqueue(
            MockResponse().setResponseCode(409)
                .setBody("""{"error":{"code":"stale_state","message":"state sequence mismatch"}}""")
        )
        val client = RelayClient(baseUrl(), "tok")

        val err = client.cancel(AgentKey("", "w1:p1"), 3).exceptionOrNull() as RelayError

        assertEquals("stale_state", err.code)
        assertEquals(409, err.httpStatus)
    }

    // ---- phase 8: commands name the host (contracts §2.1)

    @Test
    fun commandsGoToTheAgentsHost() = runBlocking {
        repeat(3) { server.enqueue(MockResponse().setBody("""{"ok":true}""")) }
        val client = RelayClient(baseUrl(), "tok")
        val box = AgentKey("box", "w1:p1")

        assertTrue(client.answer(box, "opt-1", 3, "fp").isSuccess)
        assertTrue(client.prompt(box, "hi", 3).isSuccess)
        assertTrue(client.cancel(box, 3).isSuccess)

        assertEquals("/v1/hosts/box/agents/w1%3Ap1/answer", server.takeRequest().path)
        assertEquals("/v1/hosts/box/agents/w1%3Ap1/prompt", server.takeRequest().path)
        assertEquals("/v1/hosts/box/agents/w1%3Ap1/cancel", server.takeRequest().path)
    }

    @Test
    fun twoHostsSharingAPaneGetTwoPaths() {
        val client = RelayClient("https://relay.example", "tok")
        assertEquals("/v1/hosts/main/agents/w1%3Ap1/answer", client.commandPath(AgentKey("main", "w1:p1"), "answer"))
        assertEquals("/v1/hosts/box/agents/w1%3Ap1/answer", client.commandPath(AgentKey("box", "w1:p1"), "answer"))
        assertEquals("an older relay: the old path", "/v1/agents/w1%3Ap1/answer", client.commandPath(AgentKey("", "w1:p1"), "answer"))
    }

    @Test
    fun historyNamesTheHostWhenThereIsOne() = runBlocking {
        repeat(3) { server.enqueue(MockResponse().setBody("""{"items":[]}""")) }
        val client = RelayClient(baseUrl(), "tok")

        client.history(AgentKey("box", "w1:p1"), 20)
        client.history(AgentKey("", "w1:p1"), 20)
        client.history()

        assertEquals("/v1/history?limit=20&host=box&pane_id=w1%3Ap1", server.takeRequest().path)
        assertEquals("/v1/history?limit=20&pane_id=w1%3Ap1", server.takeRequest().path)
        assertEquals("/v1/history?limit=20", server.takeRequest().path)
    }
}
