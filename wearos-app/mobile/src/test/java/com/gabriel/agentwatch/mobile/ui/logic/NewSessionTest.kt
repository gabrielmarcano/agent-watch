package com.gabriel.agentwatch.mobile.ui.logic

import com.gabriel.agentwatch.approval.RelayErrorKind
import com.gabriel.agentwatch.model.FolderEntry
import com.gabriel.agentwatch.model.FolderListResponse
import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.SessionCreated
import com.gabriel.agentwatch.model.SessionOptionsResponse
import com.gabriel.agentwatch.network.RelayError
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
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

    @Test
    fun controllerInitialStateWithPreferredHostLoadsOptionsAndRoots() {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val transport = FakeSessionTransport()
        val controller = NewSessionController(
            transport = transport,
            scope = scope,
            initialHostId = "main",
            initialStep = 1
        )

        try {
            val state = awaitValue(controller.state) { it.options != null && it.folderListing != null }
            assertEquals(1, state.step)
            assertEquals("main", state.selectedHostId)
            assertEquals("claude", state.selectedKind)
            assertNotNull(state.options)
            assertNotNull(state.folderListing)
            assertNull(state.optionsError)
        } finally {
            controller.close()
            scope.cancel()
        }
    }

    @Test
    fun hostSwitchingResetsOptionsAndCwdAndLoadsNewHostOptions() {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val transport = FakeSessionTransport()
        transport.optionsResponses["box"] = Result.success(
            SessionOptionsResponse(kinds = listOf("opencode"), roots = listOf("/home/user/src"))
        )
        val controller = NewSessionController(
            transport = transport,
            scope = scope,
            initialHostId = "main"
        )

        try {
            awaitValue(controller.state) { it.options != null }
            controller.chooseFolder("/Users/me/Code/proj")
            controller.setLabel("custom")
            assertEquals("/Users/me/Code/proj", controller.state.value.chosenPath)

            controller.selectHost("box")
            val newState = awaitValue(controller.state) { it.selectedHostId == "box" && it.options != null }
            assertEquals("box", newState.selectedHostId)
            assertEquals("opencode", newState.selectedKind)
            assertEquals("", newState.chosenPath)
            assertEquals("", newState.label)
        } finally {
            controller.close()
            scope.cancel()
        }
    }

    @Test
    fun syncStartableHostsFallsBackWhenCurrentHostGoesOffline() {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val transport = FakeSessionTransport()
        val controller = NewSessionController(
            transport = transport,
            scope = scope,
            initialHostId = "main"
        )

        try {
            awaitValue(controller.state) { it.options != null }
            val startable = listOf(HostInfo(id = "box", name = "Linux box", online = true, herdr_online = true))

            controller.syncStartableHosts(startable)
            val updated = awaitValue(controller.state) { it.selectedHostId == "box" }
            assertEquals("box", updated.selectedHostId)
        } finally {
            controller.close()
            scope.cancel()
        }
    }

    @Test
    fun folderNavigationAndSelectionUpdatesChosenPathAndProposedName() {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val transport = FakeSessionTransport()
        transport.foldersResponses["main" to "/Users/me/Code"] = Result.success(
            FolderListResponse(
                path = "/Users/me/Code",
                entries = listOf(FolderEntry("agent-watch", "/Users/me/Code/agent-watch"))
            )
        )
        val controller = NewSessionController(
            transport = transport,
            scope = scope,
            initialHostId = "main"
        )

        try {
            awaitValue(controller.state) { it.options != null }
            controller.navigateToFolder("/Users/me/Code")
            val navState = awaitValue(controller.state) { it.viewPath == "/Users/me/Code" && it.folderListing?.path == "/Users/me/Code" }
            assertEquals(1, navState.folderListing?.entries?.size)

            controller.chooseFolder("/Users/me/Code/agent-watch")
            assertEquals("/Users/me/Code/agent-watch", controller.state.value.chosenPath)
            assertEquals("agent-watch", controller.state.value.label)
        } finally {
            controller.close()
            scope.cancel()
        }
    }

    @Test
    fun createSessionSuccessAndErrorClassification() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val transport = FakeSessionTransport()
        val controller = NewSessionController(
            transport = transport,
            scope = scope,
            initialHostId = "main"
        )

        try {
            awaitValue(controller.state) { it.options != null }
            controller.chooseFolder("/Users/me/Code/proj")

            // Successful creation
            val successRes = controller.createSession()
            assertTrue(successRes.isSuccess)
            assertEquals("pane-1", successRes.getOrNull()?.pane_id)
            assertNull(controller.state.value.createError)

            // not_allowed error
            transport.createResponse = Result.failure(RelayError("not_allowed", "directory not configured", 403))
            val notAllowedRes = controller.createSession()
            assertTrue(notAllowedRes.isFailure)
            assertEquals(RelayErrorKind.NOT_ALLOWED, controller.state.value.createError)

            // host_offline error
            transport.createResponse = Result.failure(RelayError("host_offline", "bridge is unreachable", 503))
            val hostOfflineRes = controller.createSession()
            assertTrue(hostOfflineRes.isFailure)
            assertEquals(RelayErrorKind.HOST_OFFLINE, controller.state.value.createError)
        } finally {
            controller.close()
            scope.cancel()
        }
    }

    @Test
    fun optionsLoadFailureClassifiesError() {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val transport = FakeSessionTransport()
        transport.optionsResponses["offline-host"] = Result.failure(
            RelayError("host_offline", "host offline", 503)
        )
        val controller = NewSessionController(
            transport = transport,
            scope = scope,
            initialHostId = "offline-host"
        )

        try {
            val state = awaitValue(controller.state) { it.optionsError != null }
            assertEquals(RelayErrorKind.HOST_OFFLINE, state.optionsError)
            assertNull(state.options)
        } finally {
            controller.close()
            scope.cancel()
        }
    }

    private fun <T> awaitValue(flow: StateFlow<T>, timeoutMs: Long = 3000, condition: (T) -> Boolean): T {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            val current = flow.value
            if (condition(current)) return current
            Thread.sleep(10)
        }
        throw AssertionError("Timed out waiting for condition; last value was ${flow.value}")
    }
}

private class FakeSessionTransport : SessionTransport {
    val optionsResponses = mutableMapOf<String, Result<SessionOptionsResponse>>()
    val foldersResponses = mutableMapOf<Pair<String, String?>, Result<FolderListResponse>>()
    var createResponse: Result<SessionCreated> = Result.success(
        SessionCreated(pane_id = "pane-1", host = "main", label = "agent-watch", kind = "claude")
    )

    override suspend fun loadSessionOptions(hostId: String): Result<SessionOptionsResponse> =
        optionsResponses[hostId] ?: Result.success(
            SessionOptionsResponse(kinds = listOf("claude", "opencode"), roots = listOf("/Users/me/Code"))
        )

    override suspend fun listFolders(hostId: String, path: String?): Result<FolderListResponse> =
        foldersResponses[hostId to path] ?: Result.success(
            FolderListResponse(path = path ?: "", entries = listOf(FolderEntry("proj", "${path ?: ""}/proj")))
        )

    override suspend fun createSession(
        hostId: String,
        kind: String,
        cwd: String,
        title: String?
    ): Result<SessionCreated> = createResponse
}
