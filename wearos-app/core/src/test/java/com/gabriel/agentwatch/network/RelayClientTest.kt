package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.UpdateInfo
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
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.io.File
import java.net.SocketTimeoutException
import java.security.MessageDigest

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
        assertSame(a.rest.connectionPool, a.session.connectionPool)
        assertSame(a.rest.connectionPool, a.sse.connectionPool)
        assertSame(a.rest.connectionPool, a.download.connectionPool)
        assertSame(a.rest.dispatcher, a.command.dispatcher)
        assertSame(a.rest.dispatcher, a.session.dispatcher)
        assertSame(a.rest.dispatcher, a.sse.dispatcher)
        assertSame(a.rest.dispatcher, a.download.dispatcher)
    }

    @Test
    fun sessionCallTimeoutOutlastsTheRelaysSessionBudget() {
        val callTimeout = RelayHttpClients.shared.session.callTimeoutMillis
        assertTrue("session callTimeout must beat the relay's 40 s budget, was $callTimeout", callTimeout > 40_000)
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

    // ---- new sessions: host-scoped options, folders and creation (contracts §2.1)

    @Test
    fun sessionOptionsAndFoldersGoToTheHost() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"kinds":["claude"],"roots":["/Users/me/Code"]}"""))
        server.enqueue(MockResponse().setBody("""{"path":"/Users/me/Code/a b","roots":["/Users/me/Code"],"entries":[]}"""))
        server.enqueue(MockResponse().setBody("""{"path":"","roots":["/Users/me/Code"],"entries":[]}"""))
        val client = RelayClient(baseUrl(), "tok")

        val options = client.sessionOptions("box").getOrThrow()
        assertEquals(listOf("claude"), options.kinds)
        val listed = client.folders("box", "/Users/me/Code/a b").getOrThrow()
        assertEquals("/Users/me/Code/a b", listed.path)
        client.folders("box")

        assertEquals("/v1/hosts/box/sessions/options", server.takeRequest().path)
        assertEquals("/v1/hosts/box/folders?path=%2FUsers%2Fme%2FCode%2Fa+b", server.takeRequest().path)
        assertEquals("/v1/hosts/box/folders", server.takeRequest().path)
    }

    @Test
    fun createSessionPostsTheRequestAndReadsTheCreatedPane() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """{"host":"box","pane_id":"w2:p1","workspace_id":"w2","tab_id":"t1","kind":"claude","label":"agent-watch 2"}"""
            )
        )
        val client = RelayClient(baseUrl(), "tok")

        val created = client.createSession("box", "claude", "/Users/me/Code/agent-watch", "agent-watch 2").getOrThrow()

        assertEquals("w2:p1", created.pane_id)
        assertEquals("agent-watch 2", created.label)
        val req = server.takeRequest()
        assertEquals("POST", req.method)
        assertEquals("/v1/hosts/box/sessions", req.path)
        assertEquals("Bearer tok", req.getHeader("Authorization"))
        assertEquals(
            """{"kind":"claude","cwd":"/Users/me/Code/agent-watch","label":"agent-watch 2"}""",
            req.body.readUtf8()
        )
    }

    @Test
    fun createSessionKeepsTheNotAllowedCode() = runBlocking {
        server.enqueue(
            MockResponse().setResponseCode(403)
                .setBody("""{"error":{"code":"not_allowed","message":"folder is outside the configured session roots"}}""")
        )

        val err = RelayClient(baseUrl(), "tok").createSession("box", "claude", "/etc").exceptionOrNull() as RelayError

        assertEquals("not_allowed", err.code)
        assertEquals(403, err.httpStatus)
    }

    // ---- Android updates (contracts §2.1)

    @Test
    fun updateInfoSendsBearerTokenAndParsesTheBuild() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """{"app":"phone","version_code":5,"version_name":"0.4.0","sha256":"${"a".repeat(64)}","size":42}"""
            )
        )

        val info = RelayClient(baseUrl(), "tok").updateInfo("phone").getOrThrow()

        assertEquals("phone", info.app)
        assertEquals(5L, info.version_code)
        assertEquals("0.4.0", info.version_name)
        assertEquals(42L, info.size)
        val req = server.takeRequest()
        assertEquals("/v1/updates/phone", req.path)
        assertEquals("Bearer tok", req.getHeader("Authorization"))
    }

    @Test
    fun updateInfoKeepsTheUpdateNotAvailableCode() = runBlocking {
        server.enqueue(
            MockResponse().setResponseCode(404)
                .setBody("""{"error":{"code":"update_not_available","message":"no update published"}}""")
        )

        val err = RelayClient(baseUrl(), "tok").updateInfo("watch").exceptionOrNull() as RelayError

        assertEquals("update_not_available", err.code)
        assertEquals(404, err.httpStatus)
    }

    @Test
    fun downloadVerifiesTheChecksumAndWritesTheFile() = runBlocking {
        val bytes = "the apk bytes".toByteArray()
        val info = updateInfoFor(bytes)
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/vnd.android.package-archive")
                .setBody(String(bytes, Charsets.UTF_8))
        )
        val dir = File(System.getProperty("java.io.tmpdir"), "aw-update-${System.nanoTime()}")
        val dest = File(dir, "phone-5.apk")
        val progress = mutableListOf<Pair<Long, Long>>()

        try {
            val file = RelayClient(baseUrl(), "tok")
                .downloadUpdate("phone", info, dest) { read, total -> progress += read to total }
                .getOrThrow()

            assertEquals(dest, file)
            assertTrue(dest.readBytes().contentEquals(bytes))
            assertEquals(bytes.size.toLong() to bytes.size.toLong(), progress.last())
            val req = server.takeRequest()
            assertEquals("/v1/updates/phone/apk", req.path)
            assertEquals("Bearer tok", req.getHeader("Authorization"))
            assertFalse(File(dir, "phone-5.apk.part").exists())
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun downloadRejectsAChecksumMismatchAndLeavesNoFile() = runBlocking {
        val bytes = "the apk bytes".toByteArray()
        val info = updateInfoFor(bytes).copy(sha256 = "0".repeat(64))
        server.enqueue(MockResponse().setBody(String(bytes, Charsets.UTF_8)))
        val dir = File(System.getProperty("java.io.tmpdir"), "aw-update-${System.nanoTime()}")
        val dest = File(dir, "phone-5.apk")

        try {
            val err = RelayClient(baseUrl(), "tok")
                .downloadUpdate("phone", info, dest)
                .exceptionOrNull() as RelayError

            assertEquals("bad_download", err.code)
            assertFalse(dest.exists())
            assertFalse(File(dir, "phone-5.apk.part").exists())
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun downloadRejectsAShortBody() = runBlocking {
        val bytes = "the apk bytes".toByteArray()
        val info = updateInfoFor(bytes).copy(size = bytes.size.toLong() + 1)
        server.enqueue(MockResponse().setBody(String(bytes, Charsets.UTF_8)))
        val dir = File(System.getProperty("java.io.tmpdir"), "aw-update-${System.nanoTime()}")
        val dest = File(dir, "phone-5.apk")

        try {
            val err = RelayClient(baseUrl(), "tok")
                .downloadUpdate("phone", info, dest)
                .exceptionOrNull() as RelayError

            assertEquals("bad_download", err.code)
            assertFalse(dest.exists())
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun downloadReportsUnauthorizedWithTheRejectedToken() = runBlocking {
        val bytes = "the apk bytes".toByteArray()
        var rejected: String? = null
        server.enqueue(
            MockResponse().setResponseCode(401)
                .setBody("""{"error":{"code":"unauthorized","message":"bad token"}}""")
        )
        val dir = File(System.getProperty("java.io.tmpdir"), "aw-update-${System.nanoTime()}")
        val dest = File(dir, "phone-5.apk")

        try {
            val err = RelayClient(baseUrl(), "tok", onUnauthorized = { rejected = it })
                .downloadUpdate("phone", updateInfoFor(bytes), dest)
                .exceptionOrNull() as RelayError

            assertEquals("unauthorized", err.code)
            assertEquals("tok", rejected)
            assertFalse(dest.exists())
        } finally {
            dir.deleteRecursively()
        }
    }

    private fun updateInfoFor(bytes: ByteArray): UpdateInfo = UpdateInfo(
        app = "phone",
        version_code = 5,
        version_name = "0.4.0",
        sha256 = MessageDigest.getInstance("SHA-256")
            .digest(bytes)
            .joinToString("") { "%02x".format(it.toInt() and 0xff) },
        size = bytes.size.toLong()
    )
}
