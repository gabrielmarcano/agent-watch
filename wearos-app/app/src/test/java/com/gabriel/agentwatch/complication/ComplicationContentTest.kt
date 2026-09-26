package com.gabriel.agentwatch.complication

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.IOException

class ComplicationContentTest {

    private fun agent(pane: String, status: String, updated: String = "2026-09-25T18:00:00Z") =
        AgentState(pane_id = pane, label = "label-$pane", agent = "claude", status = status, updated_at = updated)

    private fun loaded(vararg agents: AgentState, host: Boolean = true) =
        Result.success(AgentsSnapshot(host_online = host, herdr_online = true, agents = agents.toList()))

    @Test
    fun unpairedOfflineAndErrorAreDistinct() {
        assertEquals(ComplicationContent(ComplicationKind.NOT_PAIRED, 0), complicationContent(paired = false, result = null))
        assertEquals(ComplicationContent(ComplicationKind.UNREACHABLE, 0), complicationContent(true, Result.failure(IOException())))
        assertEquals(ComplicationContent(ComplicationKind.DEVICE_OFFLINE, 0), complicationContent(true, loaded(agent("a", "blocked"), host = false)))
    }

    @Test
    fun needsYouCountsBlockedAndPointsAtTheFirstOne() {
        val content = complicationContent(true, loaded(agent("a", "idle"), agent("b", "blocked"), agent("c", "done"), agent("d", "blocked")))
        assertEquals(ComplicationContent(ComplicationKind.NEEDS_YOU, 2, paneId = "b", label = "label-b", agent = "claude"), content)
    }

    @Test
    fun doneCountsAndPointsAtTheLatest() {
        val content = complicationContent(
            true,
            loaded(agent("a", "done", "2026-09-25T18:00:00Z"), agent("b", "done", "2026-09-25T18:05:00Z"), agent("c", "working"))
        )
        assertEquals(ComplicationContent(ComplicationKind.DONE, 2, paneId = "b", label = "label-b", agent = "claude"), content)
    }

    @Test
    fun workingAndIdleOpenTheList() {
        assertEquals(ComplicationContent(ComplicationKind.WORKING, 2), complicationContent(true, loaded(agent("a", "working"), agent("b", "idle"), agent("c", "working"))))
        assertEquals(ComplicationContent(ComplicationKind.IDLE, 2), complicationContent(true, loaded(agent("a", "idle"), agent("b", "unknown"))))
    }

    @Test
    fun noAgents() {
        assertEquals(ComplicationContent(ComplicationKind.NO_AGENTS, 0), complicationContent(true, loaded()))
    }

    // Style B (owner's choice, 2026-09-25): the glyph and how many agents need the user, no words.
    @Test
    fun theBadgeIsTheGlyphAndHowManyNeedTheUser() {
        assertEquals(ComplicationBadge(BadgeIcon.ALERT, "2"), complicationBadge(ComplicationContent(ComplicationKind.NEEDS_YOU, 2)))
        for (kind in listOf(ComplicationKind.DONE, ComplicationKind.WORKING, ComplicationKind.IDLE, ComplicationKind.NO_AGENTS)) {
            assertEquals("$kind", ComplicationBadge(BadgeIcon.AGENT, "0"), complicationBadge(ComplicationContent(kind, 3)))
        }
    }

    @Test
    fun problemsShowTheirOwnGlyphAndADash() {
        assertEquals(ComplicationBadge(BadgeIcon.NOT_PAIRED, "–"), complicationBadge(ComplicationContent(ComplicationKind.NOT_PAIRED, 0)))
        assertEquals(ComplicationBadge(BadgeIcon.UNREACHABLE, "–"), complicationBadge(ComplicationContent(ComplicationKind.UNREACHABLE, 0)))
        assertEquals(ComplicationBadge(BadgeIcon.DEVICE_OFFLINE, "–"), complicationBadge(ComplicationContent(ComplicationKind.DEVICE_OFFLINE, 0)))
    }
}
