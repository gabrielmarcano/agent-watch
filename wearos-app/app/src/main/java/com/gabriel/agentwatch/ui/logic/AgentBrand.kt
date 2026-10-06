package com.gabriel.agentwatch.ui.logic

import androidx.annotation.DrawableRes
import com.gabriel.agentwatch.R

/** How the watch identifies an agent type: a small logo and the name its content description reads. */
data class AgentBrand(@param:DrawableRes val icon: Int, val name: String)

/**
 * The logo for herdr's `agent` id (`contracts.md` §1): the priority agents get their own, any other
 * agent (the generic adapter) the neutral agent glyph with its id as the name. Which logos are the
 * brands' own and which are our monograms: `ARCHITECTURE.md` §4b.
 */
fun agentBrand(agent: String): AgentBrand {
    val id = agent.trim()
    return when (id.lowercase()) {
        "claude" -> AgentBrand(R.drawable.ic_agent_claude, "Claude")
        "agy" -> AgentBrand(R.drawable.ic_agent_agy, "Antigravity")
        "opencode" -> AgentBrand(R.drawable.ic_agent_opencode, "OpenCode")
        else -> AgentBrand(R.drawable.ic_agent, id)
    }
}
