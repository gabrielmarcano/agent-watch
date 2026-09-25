package com.gabriel.agentwatch.network

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
        val res = withTimeoutOrNull(300) { client.answer("w1:p1", "opt-1", 5, "fp") }

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
        val err = client.prompt("w1:p1", "hi", 1).exceptionOrNull()

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

        val res = client.answer("w5:pAE", "opt-1", 334, "9f2c")

        assertTrue(res.isSuccess)
        val req = server.takeRequest()
        assertEquals("/v1/agents/w5%3ApAE/answer", req.path)
        assertEquals("Bearer tok", req.getHeader("Authorization"))
    }

    @Test
    fun relayErrorBodyIsMapped() = runBlocking {
        server.enqueue(
            MockResponse().setResponseCode(409)
                .setBody("""{"error":{"code":"stale_state","message":"state sequence mismatch"}}""")
        )
        val client = RelayClient(baseUrl(), "tok")

        val err = client.cancel("w1:p1", 3).exceptionOrNull() as RelayError

        assertEquals("stale_state", err.code)
        assertEquals(409, err.httpStatus)
    }
}
