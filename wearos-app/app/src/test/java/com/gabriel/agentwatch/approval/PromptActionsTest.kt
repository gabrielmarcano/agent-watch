package com.gabriel.agentwatch.approval

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.PendingPrompt
import com.gabriel.agentwatch.model.PromptOption
import com.google.gson.Gson
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

class PromptActionsTest {

    private fun opt(id: String, role: String, label: String = id) = PromptOption(id = id, label = label, role = role)

    private fun permission(vararg options: PromptOption) = PendingPrompt(
        kind = "permission",
        title = "Bash command",
        options = options.toList(),
        fingerprint = "fp"
    )

    @Test
    fun allowUsesAllowOnceOnGoldenPrompt() {
        val agent = Gson().fromJson(File("../../pkg/model/testdata/agent_state.json").readText(), AgentState::class.java)
        val buttons = permissionButtons(agent.prompt!!)
        assertEquals("opt-1", buttons.allow?.id)
        assertEquals("allow_once", buttons.allow?.role)
        assertEquals("opt-3", buttons.deny?.id)
        assertTrue("allow_always must stay reachable through More", buttons.hasMore)
    }

    @Test
    fun allowNeverFallsBackToAllowAlways() {
        val buttons = permissionButtons(
            permission(
                opt("opt-1", "allow_always", "Yes, and don't ask again"),
                opt("opt-2", "deny", "No")
            )
        )
        assertNull("ALLOW must not send \"don't ask again\"", buttons.allow)
        assertEquals("opt-2", buttons.deny?.id)
        assertTrue("allow_always must stay reachable through More", buttons.hasMore)
    }

    @Test
    fun allowOnceIsFoundByRoleNotPosition() {
        val buttons = permissionButtons(
            permission(
                opt("opt-1", "allow_always"),
                opt("opt-2", "deny"),
                opt("opt-3", "allow_once")
            )
        )
        assertEquals("opt-3", buttons.allow?.id)
        assertEquals("opt-2", buttons.deny?.id)
    }

    @Test
    fun twoOptionPromptNeedsNoMore() {
        val buttons = permissionButtons(permission(opt("opt-1", "allow_once"), opt("opt-2", "deny")))
        assertEquals("opt-1", buttons.allow?.id)
        assertEquals("opt-2", buttons.deny?.id)
        assertFalse(buttons.hasMore)
    }

    @Test
    fun missingDenyLeavesDenyNull() {
        val buttons = permissionButtons(permission(opt("opt-1", "allow_once"), opt("opt-2", "choice")))
        assertEquals("opt-1", buttons.allow?.id)
        assertNull(buttons.deny)
        assertTrue(buttons.hasMore)
    }

    /** Every ordering of every subset of roles: ALLOW is allow_once or nothing, and no option is unreachable. */
    @Test
    fun invariantsHoldForEveryRoleCombination() {
        val roles = listOf("allow_once", "allow_always", "deny", "choice")
        for (combo in orderedSubsets(roles)) {
            val options = combo.mapIndexed { i, role -> opt("opt-${i + 1}", role) }
            val buttons = permissionButtons(permission(*options.toTypedArray()))

            val allowRole = buttons.allow?.role
            assertTrue("$combo: ALLOW mapped to $allowRole", allowRole == null || allowRole == "allow_once")
            assertEquals("$combo: ALLOW presence", combo.contains("allow_once"), buttons.allow != null)

            val shown = setOfNotNull(buttons.allow?.id, buttons.deny?.id)
            val hidden = options.filterNot { it.id in shown }
            assertEquals("$combo: More needed", hidden.isNotEmpty(), buttons.hasMore)
        }
    }

    private fun orderedSubsets(items: List<String>): List<List<String>> {
        if (items.isEmpty()) return listOf(emptyList())
        val result = mutableListOf<List<String>>()
        for (item in items) {
            val rest = items - item
            result.add(listOf(item))
            for (tail in orderedSubsets(rest)) {
                if (tail.isNotEmpty()) result.add(listOf(item) + tail)
            }
        }
        return result.distinct()
    }
}
