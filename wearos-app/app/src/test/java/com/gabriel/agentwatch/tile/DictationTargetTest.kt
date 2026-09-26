package com.gabriel.agentwatch.tile

import com.gabriel.agentwatch.model.AgentState
import org.junit.Assert.assertEquals
import org.junit.Test

class DictationTargetTest {

    private val a = AgentState(pane_id = "w1:pA", label = "A", status = "idle", focused = true)
    private val b = AgentState(pane_id = "w1:pB", label = "B", status = "done", updated_at = "2026-09-25T18:00:00Z")

    @Test
    fun thePaneTheTileShowedWins() {
        assertEquals(DictationTarget.Found(a), dictationTarget(listOf(a, b), launchPaneId = "w1:pA", pinnedPaneId = "w1:pB"))
    }

    @Test
    fun aClosedTilePaneNeverFallsBackToAnotherAgent() {
        // The tile said "To: C"; sending to A or B instead would be a silent misdelivery.
        assertEquals(DictationTarget.Closed, dictationTarget(listOf(a, b), launchPaneId = "w1:pC", pinnedPaneId = "w1:pA"))
    }

    @Test
    fun withoutALaunchPaneTheTargetRuleApplies() {
        // pinned → latest done → focused (HERDR_REFACTOR_PLAN §11)
        assertEquals(DictationTarget.Found(b), dictationTarget(listOf(a, b), launchPaneId = null, pinnedPaneId = "w1:pB"))
        assertEquals(DictationTarget.Found(b), dictationTarget(listOf(a, b), launchPaneId = "", pinnedPaneId = null))
        assertEquals(DictationTarget.Found(a), dictationTarget(listOf(a), launchPaneId = null, pinnedPaneId = null))
    }

    @Test
    fun noAgentsNoTarget() {
        assertEquals(DictationTarget.None, dictationTarget(emptyList(), launchPaneId = null, pinnedPaneId = null))
    }
}
