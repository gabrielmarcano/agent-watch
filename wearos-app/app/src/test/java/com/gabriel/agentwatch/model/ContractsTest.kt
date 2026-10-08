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
        assertEquals("my-app", state.label)
        assertEquals("my-app", state.name)
        assertEquals("Fix the login loop", state.title)
        assertEquals("/Users/me/Code/app", state.cwd)
        assertEquals("w5", state.workspace_id)
        assertEquals("work", state.workspace)
        assertEquals("blocked", state.status)
        assertFalse(state.focused)
        assertEquals(334L, state.state_change_seq)
        assertEquals("2026-09-23T17:04:05Z", state.updated_at)
        assertEquals(4, state.severity())
        assertEquals(0, state.background_agents) // omitted in the golden (omitempty)

        val prompt = state.prompt
        assertNotNull(prompt)
        assertEquals("permission", prompt!!.kind)
        assertEquals("Bash command", prompt.title)
        assertEquals("go test ./...", prompt.detail)
        assertEquals("fd6ff7388739252d", prompt.fingerprint)
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
    fun backgroundAgentsParses() {
        // contracts §1.2: a working agent that only waits on its background agents.
        val state = Gson().fromJson(
            """{"pane_id":"w5:pAE","agent":"claude","label":"x","workspace_id":"w5","status":"working","focused":false,"state_change_seq":7,"background_agents":2,"updated_at":"2026-10-05T23:45:11Z"}""",
            AgentState::class.java
        )
        assertEquals(2, state.background_agents)
    }

    @Test
    fun backgroundTasksParse() {
        // contracts §1.2: a done agent with a shell and a monitor still running.
        val state = Gson().fromJson(
            """{"pane_id":"w5:pAE","agent":"claude","label":"x","workspace_id":"w5","status":"done","focused":false,"state_change_seq":7,"background_shells":1,"background_monitors":2,"updated_at":"2026-10-07T20:00:00Z"}""",
            AgentState::class.java
        )
        assertEquals(1, state.background_shells)
        assertEquals(2, state.background_monitors)
    }

    @Test
    fun optionDescriptionIsOptional() {
        // contracts §1.3: description is omitempty; the golden has none.
        val options = Gson().fromJson(
            """[{"id":"opt-1","label":"Verde","description":"Green","role":"choice"},{"id":"opt-2","label":"No","role":"deny"}]""",
            Array<PromptOption>::class.java
        )
        assertEquals("Green", options[0].description)
        assertEquals(null, options[1].description)
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

        // 5. No pinned, no done, no focused -> no target (ARCHITECTURE.md §4b): never an arbitrary first agent
        val listNoFocused = listOf(a1)
        assertEquals(null, resolveTargetAgent(listNoFocused, null))
        assertEquals(null, resolveTargetAgent(listNoFocused, "gone"))

        // 6. Empty list -> null
        assertEquals(null, resolveTargetAgent(emptyList(), "p1"))
    }
}
