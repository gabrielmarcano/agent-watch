package com.gabriel.agentwatch.network

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * contracts §4.1: FCM does not guarantee order, so a `blocked` push whose seq is not newer than the
 * last `resolved` seen for its pane is stale and must not be shown.
 */
class ResolvedSeqsTest {

    @Test
    fun blockedIsShownWhenNothingWasResolved() {
        assertTrue(shouldShowBlocked(seq = 334, lastResolved = null))
    }

    @Test
    fun blockedAtOrBelowTheLastResolvedSeqIsStale() {
        assertFalse(shouldShowBlocked(seq = 334, lastResolved = 335))
        assertFalse(shouldShowBlocked(seq = 335, lastResolved = 335))
    }

    @Test
    fun blockedNewerThanTheLastResolvedSeqIsShown() {
        assertTrue(shouldShowBlocked(seq = 336, lastResolved = 335))
    }

    @Test
    fun recordKeepsTheHighestSeqPerPane() {
        val seqs = ResolvedSeqs()
            .record("w5:pAE", 335)
            .record("w5:pAE", 330) // a late, older resolved must not lower it
            .record("w9:p1", 12)
        assertEquals(335L, seqs.lastFor("w5:pAE"))
        assertEquals(12L, seqs.lastFor("w9:p1"))
        assertNull(seqs.lastFor("w1:p1"))
    }

    @Test
    fun memoryIsBoundedDroppingTheLeastRecentlyRecordedPane() {
        var seqs = ResolvedSeqs(maxPanes = 3)
        for (i in 1..4) seqs = seqs.record("w1:p$i", i.toLong())
        assertNull(seqs.lastFor("w1:p1"))
        assertEquals(4L, seqs.lastFor("w1:p4"))
        assertEquals(3, seqs.size)
    }

    @Test
    fun encodeDecodeRoundTrips() {
        val seqs = ResolvedSeqs().record("w5:pAE", 335).record("w9:p1", 12)
        val back = ResolvedSeqs.decode(seqs.encode())
        assertEquals(335L, back.lastFor("w5:pAE"))
        assertEquals(12L, back.lastFor("w9:p1"))
    }

    @Test
    fun garbageOrMissingDecodesToEmpty() {
        assertEquals(0, ResolvedSeqs.decode(null).size)
        assertEquals(0, ResolvedSeqs.decode("not json").size)
        assertEquals(0, ResolvedSeqs.decode("{\"w1:p1\":\"x\"}").size)
    }
}
