package com.gabriel.agentwatch.ui.logic

import com.gabriel.agentwatch.R
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test

class AgentBrandTest {

    @Test
    fun priorityAgentsGetTheirOwnLogoAndName() {
        assertEquals(AgentBrand(R.drawable.ic_agent_claude, "Claude"), agentBrand("claude"))
        assertEquals(AgentBrand(R.drawable.ic_agent_agy, "Antigravity"), agentBrand("agy"))
        assertEquals(AgentBrand(R.drawable.ic_agent_opencode, "OpenCode"), agentBrand("opencode"))
    }

    @Test
    fun herdrIdsMapToTheirProductsMark() {
        // The id is herdr's, not the brand's file name: codex uses OpenAI's mark, copilot GitHub Copilot's.
        assertEquals(AgentBrand(R.drawable.ic_agent_codex, "Codex"), agentBrand("codex"))
        assertEquals(AgentBrand(R.drawable.ic_agent_copilot, "GitHub Copilot"), agentBrand("copilot"))
        assertEquals(AgentBrand(R.drawable.ic_agent_qodercli, "Qoder"), agentBrand("qodercli"))
        assertEquals(AgentBrand(R.drawable.ic_agent_mastracode, "Mastra Code"), agentBrand("mastracode"))
    }

    @Test
    fun everyBrandedHerdrKindHasItsOwnLogo() {
        val kinds = listOf(
            "pi", "claude", "codex", "gemini", "cursor", "devin", "agy", "cline", "mastracode", "opencode",
            "copilot", "kimi", "kiro", "amp", "grok", "hermes", "kilo", "qodercli", "qwen"
        )
        kinds.forEach { assertNotEquals(it, R.drawable.ic_agent, agentBrand(it).icon) }
        assertEquals(kinds.size, kinds.map { agentBrand(it).icon }.toSet().size)
    }

    @Test
    fun theIdIsMatchedIgnoringCaseAndSpaces() {
        assertEquals(R.drawable.ic_agent_claude, agentBrand(" Claude ").icon)
    }

    @Test
    fun anUnknownAgentGetsTheGenericGlyphNamedByItsId() {
        assertEquals(AgentBrand(R.drawable.ic_agent, "droid"), agentBrand("droid"))
        assertEquals(AgentBrand(R.drawable.ic_agent, "something-new"), agentBrand("something-new"))
    }

    @Test
    fun aBlankAgentGetsTheGenericGlyphWithNoName() {
        assertEquals(AgentBrand(R.drawable.ic_agent, ""), agentBrand(""))
        assertEquals(AgentBrand(R.drawable.ic_agent, ""), agentBrand("   "))
    }
}
