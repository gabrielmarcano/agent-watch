package com.gabriel.agentwatch.tile

import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import org.junit.Assert.assertEquals
import org.junit.Test

class DictationTargetTest {

    private val a = AgentState(pane_id = "w1:pA", label = "A", status = "idle", focused = true)
    private val b = AgentState(pane_id = "w1:pB", label = "B", status = "done", updated_at = "2026-09-25T18:00:00Z")

    @Test
    fun thePaneTheTileShowedWins() {
        assertEquals(DictationTarget.Found(a), dictationTarget(listOf(a, b), launch = AgentKey("", "w1:pA"), pinned = AgentKey("", "w1:pB")))
    }

    @Test
    fun aClosedTilePaneNeverFallsBackToAnotherAgent() {
        // The tile said "To: C"; sending to A or B instead would be a silent misdelivery.
        assertEquals(DictationTarget.Closed, dictationTarget(listOf(a, b), launch = AgentKey("", "w1:pC"), pinned = AgentKey("", "w1:pA")))
    }

    @Test
    fun withoutALaunchPaneTheTargetRuleApplies() {
        // pinned → latest done → focused (wearos-app/ARCHITECTURE.md §4b)
        assertEquals(DictationTarget.Found(b), dictationTarget(listOf(a, b), launch = null, pinned = AgentKey("", "w1:pB")))
        assertEquals(DictationTarget.Found(b), dictationTarget(listOf(a, b), launch = AgentKey("", ""), pinned = null))
        assertEquals(DictationTarget.Found(a), dictationTarget(listOf(a), launch = null, pinned = null))
    }

    @Test
    fun noAgentsNoTarget() {
        assertEquals(DictationTarget.None, dictationTarget(emptyList(), launch = null, pinned = null))
    }

    @Test
    fun theTilesAgentIsTheOneOnItsHost() {
        val onMac = a.copy(host = "main")
        val onBox = a.copy(host = "box", focused = false)
        assertEquals(DictationTarget.Found(onBox), dictationTarget(listOf(onMac, onBox), launch = AgentKey("box", "w1:pA"), pinned = null))
        assertEquals(DictationTarget.Closed, dictationTarget(listOf(onMac), launch = AgentKey("box", "w1:pA"), pinned = null))
        // A tile drawn before hosts names no host: never guess between two hosts.
        assertEquals(DictationTarget.Closed, dictationTarget(listOf(onMac, onBox), launch = AgentKey("", "w1:pA"), pinned = null))
        assertEquals(DictationTarget.Found(onBox), dictationTarget(listOf(onBox), launch = AgentKey("", "w1:pA"), pinned = null))
    }
}
