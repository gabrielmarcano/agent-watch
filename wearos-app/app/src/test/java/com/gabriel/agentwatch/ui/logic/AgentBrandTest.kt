package com.gabriel.agentwatch.ui.logic

import com.gabriel.agentwatch.R
import org.junit.Assert.assertEquals
import org.junit.Test

class AgentBrandTest {

    @Test
    fun priorityAgentsGetTheirOwnLogoAndName() {
        assertEquals(AgentBrand(R.drawable.ic_agent_claude, "Claude"), agentBrand("claude"))
        assertEquals(AgentBrand(R.drawable.ic_agent_agy, "Antigravity"), agentBrand("agy"))
        assertEquals(AgentBrand(R.drawable.ic_agent_opencode, "OpenCode"), agentBrand("opencode"))
    }

    @Test
    fun theIdIsMatchedIgnoringCaseAndSpaces() {
        assertEquals(R.drawable.ic_agent_claude, agentBrand(" Claude ").icon)
    }

    @Test
    fun anUnknownAgentGetsTheGenericGlyphNamedByItsId() {
        assertEquals(AgentBrand(R.drawable.ic_agent, "codex"), agentBrand("codex"))
    }

    @Test
    fun aBlankAgentGetsTheGenericGlyphWithNoName() {
        assertEquals(AgentBrand(R.drawable.ic_agent, ""), agentBrand(""))
        assertEquals(AgentBrand(R.drawable.ic_agent, ""), agentBrand("   "))
    }
}
