package com.gabriel.agentwatch.ui.logic

import androidx.annotation.DrawableRes
import com.gabriel.agentwatch.R

/** How the watch identifies an agent type: a small logo and the name its content description reads. */
data class AgentBrand(@param:DrawableRes val icon: Int, val name: String)

/**
 * herdr's agent ids (`herdr agent`'s `kinds:`, herdr 0.9.3) that have a brand mark, with the
 * product's name. The marks come from lobe-icons (`ARCHITECTURE.md` §4b). herdr's other kinds
 * (`omp`, `droid`, `letta`, `maki`, `muse`) and anything unknown get the generic glyph.
 */
private val BRANDS: Map<String, AgentBrand> = mapOf(
    "amp" to AgentBrand(R.drawable.ic_agent_amp, "Amp"),
    "agy" to AgentBrand(R.drawable.ic_agent_agy, "Antigravity"),
    "claude" to AgentBrand(R.drawable.ic_agent_claude, "Claude"),
    "cline" to AgentBrand(R.drawable.ic_agent_cline, "Cline"),
    "codex" to AgentBrand(R.drawable.ic_agent_codex, "Codex"),
    "copilot" to AgentBrand(R.drawable.ic_agent_copilot, "GitHub Copilot"),
    "cursor" to AgentBrand(R.drawable.ic_agent_cursor, "Cursor"),
    "devin" to AgentBrand(R.drawable.ic_agent_devin, "Devin"),
    "gemini" to AgentBrand(R.drawable.ic_agent_gemini, "Gemini"),
    "grok" to AgentBrand(R.drawable.ic_agent_grok, "Grok"),
    "hermes" to AgentBrand(R.drawable.ic_agent_hermes, "Hermes Agent"),
    "kilo" to AgentBrand(R.drawable.ic_agent_kilo, "Kilo Code"),
    "kimi" to AgentBrand(R.drawable.ic_agent_kimi, "Kimi"),
    "kiro" to AgentBrand(R.drawable.ic_agent_kiro, "Kiro"),
    "mastracode" to AgentBrand(R.drawable.ic_agent_mastracode, "Mastra Code"),
    "opencode" to AgentBrand(R.drawable.ic_agent_opencode, "OpenCode"),
    "pi" to AgentBrand(R.drawable.ic_agent_pi, "Pi"),
    "qodercli" to AgentBrand(R.drawable.ic_agent_qodercli, "Qoder"),
    "qwen" to AgentBrand(R.drawable.ic_agent_qwen, "Qwen"),
)

/** The logo for herdr's `agent` id (`contracts.md` §1); any other agent gets the neutral agent glyph named by its id. */
fun agentBrand(agent: String): AgentBrand {
    val id = agent.trim()
    return BRANDS[id.lowercase()] ?: AgentBrand(R.drawable.ic_agent, id)
}
