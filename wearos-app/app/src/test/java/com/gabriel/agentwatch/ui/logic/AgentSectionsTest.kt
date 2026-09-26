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
}
