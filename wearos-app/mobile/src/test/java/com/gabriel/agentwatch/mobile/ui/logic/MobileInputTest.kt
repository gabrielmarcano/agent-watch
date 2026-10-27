package com.gabriel.agentwatch.mobile.ui.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class MobileInputTest {

    @Test
    fun pairCodeKeepsOnlySixDigits() {
        assertEquals("", cleanPairCode(""))
        assertEquals("123456", cleanPairCode("123456"))
        assertEquals("123456", cleanPairCode("1234567890"))
        assertEquals("123456", cleanPairCode("12 34-56"))
        assertEquals("12345", cleanPairCode("12a3b45c"))
    }

    @Test
    fun onlyASixDigitCodeIsComplete() {
        assertTrue(pairCodeComplete("123456"))
        assertFalse(pairCodeComplete("12345"))
        assertFalse(pairCodeComplete(""))
        assertFalse(pairCodeComplete("1234567"))
    }

    @Test
    fun anEmptyReplyIsNeverSent() {
        assertNull(replyToSend(""))
        assertNull(replyToSend("   "))
        assertNull(replyToSend("\n\t "))
        assertEquals("go on", replyToSend("  go on  "))
        assertEquals("line one\nline two", replyToSend("line one\nline two"))
    }

    @Test
    fun deviceNameKeepsTheModel() {
        assertEquals("Google Pixel 8", pairDeviceName("Google", "Pixel 8"))
        assertEquals("Google Pixel 8", pairDeviceName("google", "Pixel 8"))
        assertEquals("Unknown", pairDeviceName("", "Unknown"))
    }
}
