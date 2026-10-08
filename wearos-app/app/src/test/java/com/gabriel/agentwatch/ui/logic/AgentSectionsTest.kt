package com.gabriel.agentwatch.ui.logic

import com.gabriel.agentwatch.model.AgentState
import org.junit.Assert.assertEquals
import org.junit.Test

class AgentSectionsTest {

    private fun agent(pane: String, status: String, workspace: String = "ws") =
        AgentState(pane_id = pane, label = pane, status = status, workspace_id = workspace)

    @Test
    fun blockedAgentsComeFirstWhateverTheirWorkspace() {
        // The store order: severity, then label. Workspaces are interleaved on purpose.
        val agents = listOf(
            agent("a-blocked", "blocked", "w3"),
            agent("b-blocked", "blocked", "w2"),
            agent("c-done", "done", "w3"),
            agent("d-working", "working", "w2"),
            agent("e-idle", "idle", "w3"),
            agent("f-idle", "idle", "w1"),
            agent("g-unknown", "unknown", "w1"),
        )

        val sections = attentionSections(agents)

        assertEquals(
            listOf(
                AttentionSection.NEEDS_YOU,
                AttentionSection.DONE,
                AttentionSection.WORKING,
                AttentionSection.IDLE,
                AttentionSection.UNKNOWN
            ),
            sections.map { it.section }
        )
        assertEquals(listOf("a-blocked", "b-blocked"), sections[0].agents.map { it.pane_id })
        assertEquals(listOf("e-idle", "f-idle"), sections[3].agents.map { it.pane_id })
    }

    @Test
    fun emptySectionsAreLeftOut() {
        val sections = attentionSections(listOf(agent("x", "idle"), agent("y", "blocked")))
        assertEquals(listOf(AttentionSection.NEEDS_YOU, AttentionSection.IDLE), sections.map { it.section })
    }

    @Test
    fun orderInsideASectionIsTheInputOrder() {
        val sections = attentionSections(listOf(agent("zeta", "idle"), agent("alpha", "idle")))
        assertEquals(listOf("zeta", "alpha"), sections.single().agents.map { it.pane_id })
    }

    @Test
    fun anUnexpectedStatusIsUnknown() {
        assertEquals(AttentionSection.UNKNOWN, agent("x", "sleeping").attentionSection())
        assertEquals(AttentionSection.UNKNOWN, agent("x", "").attentionSection())
    }

    @Test
    fun noAgentsNoSections() {
        assertEquals(emptyList<SectionedAgents>(), attentionSections(emptyList()))
    }

    @Test
    fun aWorkingAgentWaitingOnBackgroundAgentsShowsAsDoneButStaysInWorking() {
        val waiting = agent("w", "working").copy(background_agents = 2)
        assertEquals(2, waiting.waitingOnBackground())
        assertEquals("done", waiting.shownStatus())
        assertEquals(AttentionSection.WORKING, waiting.attentionSection())

        val generating = agent("g", "working")
        assertEquals(0, generating.waitingOnBackground())
        assertEquals("working", generating.shownStatus())

        // The count means nothing outside working (the bridge never sends it there).
        val blocked = agent("b", "blocked").copy(background_agents = 1)
        assertEquals(0, blocked.waitingOnBackground())
        assertEquals("blocked", blocked.shownStatus())
    }

    @Test
    fun shellsAndMonitorsCountWithAnyStatusAndChangeNoStatus() {
        val done = agent("d", "done").copy(background_shells = 1, background_monitors = 2)
        assertEquals(BackgroundCounts(agents = 0, shells = 1, monitors = 2), done.backgroundCounts())
        assertEquals("done", done.shownStatus())

        // A working agent still generating keeps its status: shells say nothing about its turn.
        val generating = agent("g", "working").copy(background_shells = 1)
        assertEquals("working", generating.shownStatus())
        assertEquals(BackgroundCounts(0, 1, 0), generating.backgroundCounts())

        val waiting = agent("w", "working").copy(background_agents = 2, background_monitors = 1)
        assertEquals(BackgroundCounts(2, 0, 1), waiting.backgroundCounts())

        assertEquals(false, agent("i", "idle").backgroundCounts().any)
    }
}
