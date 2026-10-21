package com.gabriel.agentwatch.mobile.ui.logic

import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.SessionOptionsResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class NewSessionTest {

    @Test
    fun onlyHostsThatCanStartAnAgentAreOffered() {
        val hosts = listOf(
            HostInfo(id = "main", name = "Mac", online = true, herdr_online = true),
            HostInfo(id = "box", name = "Linux box", online = true, herdr_online = false),
            HostInfo(id = "off", name = "Sleeping", online = false, herdr_online = false)
        )

        assertEquals(listOf("main"), sessionHosts(hosts).map { it.id })
        assertTrue(sessionHosts(emptyList()).isEmpty())
    }

    @Test
    fun kindsAreTrimmedDeduplicatedAndBlankFree() {
        assertEquals(
            listOf("claude", "opencode"),
            sessionKinds(SessionOptionsResponse(kinds = listOf(" claude ", "claude", "", "opencode")))
        )
        assertTrue(sessionKinds(null).isEmpty())
        assertTrue(sessionKinds(SessionOptionsResponse()).isEmpty())
    }

    @Test
    fun theProposedNameIsTheFoldersOwnName() {
        assertEquals("agent-watch", defaultSessionLabel("/Users/me/Code/agent-watch"))
        assertEquals("agent-watch", defaultSessionLabel("/Users/me/Code/agent-watch/"))
        assertEquals("Code", defaultSessionLabel("/Users/me/Code"))
        // A host root has no name of its own.
        assertEquals("session", defaultSessionLabel("/"))
        assertEquals("session", defaultSessionLabel(""))
        assertEquals("session", defaultSessionLabel(null))
        assertEquals("session", defaultSessionLabel("   "))
    }

    @Test
    fun createNeedsAHostAKindAFolderAndNoPendingRequest() {
        assertTrue(canCreateSession("main", "claude", "/Users/me/Code", creating = false))
        assertFalse(canCreateSession(null, "claude", "/Users/me/Code", creating = false))
        assertFalse(canCreateSession("main", null, "/Users/me/Code", creating = false))
        assertFalse(canCreateSession("main", "claude", null, creating = false))
        assertFalse(canCreateSession("main", "claude", "  ", creating = false))
        assertFalse(canCreateSession("main", "claude", "/Users/me/Code", creating = true))
    }
}
