package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.data.FcmRegistrationRecord
import com.gabriel.agentwatch.data.FcmRegistrationStore
import com.gabriel.agentwatch.data.needsFcmRegistration
import com.gabriel.agentwatch.data.pairingBinding
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/** Item 4: the "registered" flag is written only after the relay accepted the FCM token, and failures retry. */
class FcmRegistrarTest {
    private class MemoryStore(
        @Volatile override var relayUrl: String,
        @Volatile override var deviceToken: String?,
        @Volatile override var fcmToken: String?
    ) : FcmRegistrationStore {
        @Volatile override var fcmRegistration: FcmRegistrationRecord? = null
    }

    private lateinit var server: MockWebServer
    private lateinit var store: MemoryStore
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    private fun registrar(retryDelaysMs: List<Long> = listOf(60_000)) =
        FcmRegistrar(store, scope, retryDelaysMs = retryDelaysMs, newClient = { url, token -> RelayClient(url, token) })

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        store = MemoryStore(server.url("/").toString(), "device-token", "fcm-1")
    }

    @After
    fun tearDown() {
        scope.cancel()
        server.shutdown()
    }

    @Test
    fun theFlagIsSetOnlyAfterTheRelayAcceptsTheToken() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(500))

        assertEquals(FcmRegistrar.Outcome.FAILED, registrar().ensureRegistered())
        assertNull("a failed registration must not be recorded", store.fcmRegistration)

        server.enqueue(MockResponse().setBody("""{"ok":true}"""))
        assertEquals(FcmRegistrar.Outcome.REGISTERED, registrar().ensureRegistered())
        assertEquals("fcm-1", store.fcmRegistration?.fcmToken)

        val body = server.takeRequest().let { server.takeRequest() }.body.readUtf8()
        assertEquals("""{"platform":"fcm","token":"fcm-1"}""", body)
    }

    @Test
    fun alreadyRegisteredForThisPairingSendsNothing() = runBlocking {
        store.fcmRegistration = FcmRegistrationRecord("fcm-1", pairingBinding(store.relayUrl, "device-token"))

        assertEquals(FcmRegistrar.Outcome.ALREADY_REGISTERED, registrar().ensureRegistered())
        assertEquals(0, server.requestCount)
    }

    @Test
    fun aFailureIsRetriedLater() {
        server.enqueue(MockResponse().setResponseCode(503))
        server.enqueue(MockResponse().setBody("""{"ok":true}"""))

        runBlocking { registrar(retryDelaysMs = listOf(50)).ensureRegistered() }

        awaitTrue(what = "retried registration") { store.fcmRegistration?.fcmToken == "fcm-1" }
        assertEquals(2, server.requestCount)
    }

    @Test
    fun a401IsNotRetried() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(401))

        assertEquals(FcmRegistrar.Outcome.REJECTED, registrar(retryDelaysMs = listOf(50)).ensureRegistered())
        Thread.sleep(300)

        assertEquals(1, server.requestCount)
        assertNull(store.fcmRegistration)
    }

    @Test
    fun nothingToDoWithoutPairingOrFcmToken() = runBlocking {
        store.fcmToken = null
        assertEquals(FcmRegistrar.Outcome.NOT_READY, registrar().ensureRegistered())
        store.fcmToken = "fcm-1"
        store.deviceToken = null
        assertEquals(FcmRegistrar.Outcome.NOT_READY, registrar().ensureRegistered())
        assertEquals(0, server.requestCount)
    }

    @Test
    fun registrationNeedsARecordForThisExactTokenAndPairing() {
        val url = "https://relay.example.com"
        val registered = FcmRegistrationRecord("fcm-1", pairingBinding(url, "dev-1"))

        assertFalse(needsFcmRegistration("fcm-1", url, "dev-1", registered))
        assertTrue("new FCM token", needsFcmRegistration("fcm-2", url, "dev-1", registered))
        assertTrue("re-paired: new device on the relay", needsFcmRegistration("fcm-1", url, "dev-2", registered))
        assertTrue("other relay", needsFcmRegistration("fcm-1", "https://other.example.com", "dev-1", registered))
        assertTrue("never registered", needsFcmRegistration("fcm-1", url, "dev-1", null))
        assertFalse("no FCM token yet", needsFcmRegistration(null, url, "dev-1", null))
        assertFalse("not paired", needsFcmRegistration("fcm-1", url, null, null))
    }

    @Test
    fun theBindingIsStableAndNeverContainsTheToken() {
        val a = pairingBinding("https://relay.example.com/", "0123456789abcdef")
        assertEquals(a, pairingBinding("https://relay.example.com", "0123456789abcdef"))
        assertNotEquals(a, pairingBinding("https://relay.example.com", "fedcba9876543210"))
        assertFalse(a.contains("0123456789abcdef"))
    }

    @Test
    fun recordsRoundTripThroughTheirStoredForm() {
        val record = FcmRegistrationRecord("fcm:token|with-odd-chars", "abcd1234abcd1234")
        assertEquals(record, FcmRegistrationRecord.decode(record.encode()))
        assertNull(FcmRegistrationRecord.decode("garbage"))
        assertNull(FcmRegistrationRecord.decode(null))
    }
}
