package com.gabriel.agentwatch.model

import com.google.gson.Gson
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

class ContractsTest {

    @Test
    fun testParseAgentStateGolden() {
        val file = File("../../pkg/model/testdata/agent_state.json")
        assertTrue("Golden file must exist at ${file.absolutePath}", file.exists())

        val json = file.readText()
        val gson = Gson()
        val state = gson.fromJson(json, AgentState::class.java)

        assertNotNull(state)
        assertEquals("w5:pAE", state.pane_id)
        assertEquals("claude", state.agent)
        assertEquals("bizum", state.label)
        assertEquals("bizum", state.name)
        assertEquals("/Users/me/Code/app", state.cwd)
        assertEquals("w5", state.workspace_id)
        assertEquals("bizum-app", state.workspace)
        assertEquals("blocked", state.status)
        assertFalse(state.focused)
        assertEquals(334L, state.state_change_seq)
        assertEquals("2026-09-23T17:04:05Z", state.updated_at)
        assertEquals(4, state.severity())

        val prompt = state.prompt
        assertNotNull(prompt)
        assertEquals("permission", prompt!!.kind)
        assertEquals("Bash command", prompt.title)
        assertEquals("go test ./...", prompt.detail)
        assertEquals("9f2c61d0a4b3e871", prompt.fingerprint)
        assertEquals(3, prompt.options.size)

        val opt1 = prompt.options[0]
        assertEquals("opt-1", opt1.id)
        assertEquals("Yes", opt1.label)
        assertEquals("allow_once", opt1.role)

        val opt2 = prompt.options[1]
        assertEquals("opt-2", opt2.id)
        assertEquals("Yes, and don't ask again for go test commands", opt2.label)
        assertEquals("allow_always", opt2.role)

        val opt3 = prompt.options[2]
        assertEquals("opt-3", opt3.id)
        assertEquals("No, and tell Claude what to do differently", opt3.label)
        assertEquals("deny", opt3.role)
    }

    @Test
    fun testResolveTargetAgent() {
        val a1 = AgentState(pane_id = "p1", label = "Agent 1", status = "working", updated_at = "2026-09-23T10:00:00Z")
        val a2 = AgentState(pane_id = "p2", label = "Agent 2", status = "done", updated_at = "2026-09-23T11:00:00Z")
        val a3 = AgentState(pane_id = "p3", label = "Agent 3", status = "done", updated_at = "2026-09-23T12:00:00Z")
        val a4 = AgentState(pane_id = "p4", label = "Agent 4", status = "idle", focused = true, updated_at = "2026-09-23T09:00:00Z")

        val list = listOf(a1, a2, a3, a4)

        // 1. Pinned agent matches
        assertEquals("p1", resolveTargetAgent(list, "p1")?.pane_id)

        // 2. Pinned agent not in list -> falls back to done with latest updated_at (a3)
        assertEquals("p3", resolveTargetAgent(list, "nonexistent")?.pane_id)

        // 3. No pinned agent -> falls back to done with latest updated_at (a3)
        assertEquals("p3", resolveTargetAgent(list, null)?.pane_id)

        // 4. No done agents -> falls back to focused
        val listNoDone = listOf(a1, a4)
        assertEquals("p4", resolveTargetAgent(listNoDone, null)?.pane_id)

        // 5. No pinned, no done, no focused -> no target (plan §11): never an arbitrary first agent
        val listNoFocused = listOf(a1)
        assertEquals(null, resolveTargetAgent(listNoFocused, null))
        assertEquals(null, resolveTargetAgent(listNoFocused, "gone"))

        // 6. Empty list -> null
        assertEquals(null, resolveTargetAgent(emptyList(), "p1"))
    }
}
