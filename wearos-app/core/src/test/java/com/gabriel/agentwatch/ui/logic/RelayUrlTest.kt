package com.gabriel.agentwatch.ui.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RelayUrlTest {

    @Test
    fun aBareHostGetsHttps() {
        assertEquals("https://relay.example.com", normalizeRelayUrl("relay.example.com"))
    }

    @Test
    fun spacesAndTrailingSlashesGo() {
        assertEquals("https://relay.example.com", normalizeRelayUrl("  https://relay.example.com/ "))
        // Voice input puts spaces around dots.
        assertEquals("https://relay.example.com", normalizeRelayUrl("relay . example . com"))
    }

    @Test
    fun theSchemeAndHostAreLowercased() {
        assertEquals("https://relay.example.com", normalizeRelayUrl("HTTPS://Relay.Example.com"))
    }

    @Test
    fun blankStaysBlank() {
        assertEquals("", normalizeRelayUrl("   "))
    }

    @Test
    fun onlyHttpsIsAcceptedInRelease() {
        assertTrue(isAcceptableRelayUrl("https://relay.example.com", allowCleartext = false))
        assertFalse(isAcceptableRelayUrl("http://relay.example.com", allowCleartext = false))
        assertFalse(isAcceptableRelayUrl("https://", allowCleartext = false))
        assertFalse(isAcceptableRelayUrl("", allowCleartext = false))
        assertFalse(isAcceptableRelayUrl("https://relay example.com", allowCleartext = false))
    }

    @Test
    fun debugBuildsMayUseTheEmulatorsHost() {
        assertTrue(isAcceptableRelayUrl("http://10.0.2.2:8090", allowCleartext = true))
    }

    @Test
    fun theHostIsShownWithoutTheScheme() {
        assertEquals("relay.example.com", displayHost("https://relay.example.com"))
        assertEquals("10.0.2.2:8090", displayHost("http://10.0.2.2:8090/"))
        assertEquals("", displayHost(""))
    }
}
