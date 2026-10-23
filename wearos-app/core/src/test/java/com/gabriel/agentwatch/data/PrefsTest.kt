package com.gabriel.agentwatch.data

import com.gabriel.agentwatch.model.AgentKey
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

class PrefsTest {

    @Test
    fun clearAuthClearsTokensAndSeqsWhilePreservingRelayUrlAndPinnedTarget() {
        val store = InMemoryKeyValueStore()
        val prefs = Prefs(store)

        prefs.relayUrl = "https://relay.example.com"
        prefs.deviceToken = "secret-device-token"
        prefs.deviceId = "device-123"
        prefs.fcmRegistration = FcmRegistrationRecord("fcm-tok", "binding-hash")
        prefs.pinnedTarget = AgentKey("host-a", "pane-1")
        prefs.recordResolvedSeq(AgentKey("host-a", "pane-1"), 42L)

        assertTrue(prefs.isPaired)
        assertEquals("https://relay.example.com", prefs.relayUrl)
        assertEquals("secret-device-token", prefs.deviceToken)
        assertEquals("device-123", prefs.deviceId)
        assertNotNull(prefs.fcmRegistration)
        assertEquals(AgentKey("host-a", "pane-1"), prefs.pinnedTarget)
        assertEquals(42L, prefs.resolvedSeqs.lastFor(AgentKey("host-a", "pane-1")))

        prefs.clearAuth()

        assertFalse(prefs.isPaired)
        assertNull(prefs.deviceToken)
        assertNull(prefs.deviceId)
        assertNull(prefs.fcmRegistration)
        assertEquals(0, prefs.resolvedSeqs.size)

        // Preserved
        assertEquals("https://relay.example.com", prefs.relayUrl)
        assertEquals(AgentKey("host-a", "pane-1"), prefs.pinnedTarget)
    }

    @Test
    fun legacyFieldsArePurgedOnInit() {
        val store = InMemoryKeyValueStore()
        store.putString("local_ip", "192.168.1.10")
        store.putString("tailscale_ip", "100.64.0.1")
        store.putString("fcm_registered_token", "legacy-token")
        store.putString("relay_url", "https://relay.example.com")

        Prefs(store)

        assertFalse(store.contains("local_ip"))
        assertFalse(store.contains("tailscale_ip"))
        assertFalse(store.contains("fcm_registered_token"))
        assertEquals("https://relay.example.com", store.getString("relay_url"))
    }

    @Test
    fun concurrentRecordResolvedSeqPreservesAllUpdatesWithoutLostUpdates() {
        val store = InMemoryKeyValueStore()
        val numThreads = 16
        val agentsCount = 16
        val iterationsPerThread = 50
        val executor = Executors.newFixedThreadPool(numThreads)
        val startLatch = CountDownLatch(1)
        val doneLatch = CountDownLatch(numThreads)

        val agents = (0 until agentsCount).map { AgentKey("host-$it", "pane-$it") }

        for (t in 0 until numThreads) {
            executor.execute {
                try {
                    startLatch.await()
                    val prefs = Prefs(store)
                    for (i in 1..iterationsPerThread) {
                        val agent = agents[(t + i) % agentsCount]
                        prefs.recordResolvedSeq(agent, i.toLong())
                    }
                } finally {
                    doneLatch.countDown()
                }
            }
        }

        startLatch.countDown()
        assertTrue("Timeout waiting for concurrent updates", doneLatch.await(5, TimeUnit.SECONDS))
        executor.shutdown()

        val finalPrefs = Prefs(store)
        val seqs = finalPrefs.resolvedSeqs

        assertEquals(agentsCount, seqs.size)
        for (agent in agents) {
            val recorded = seqs.lastFor(agent)
            assertNotNull("Missing sequence for agent $agent", recorded)
            assertEquals(iterationsPerThread.toLong(), recorded)
        }
    }
}
