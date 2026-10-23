package com.gabriel.agentwatch.mobile.ui

import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class MobileChatStateTest {

    private fun testAgent(
        host: String = "local",
        pane: String = "pane1",
        seq: Long = 10L
    ) = AgentState(
        agent = "claude",
        host = host,
        pane_id = pane,
        status = "idle",
        state_change_seq = seq
    )

    @Test
    fun pinMuteHideImmediatelyEmitUpdatedSnapshotsOnState() {
        val storage = InMemoryChatStateStorage()
        val chatState = MobileChatState(storage)
        val key = AgentKey("local", "pane1")

        assertFalse(chatState.isPinned(key))
        assertFalse(chatState.isMuted(key))
        assertFalse(chatState.isHidden(key))

        chatState.setPinned(key, true, label = "Agent 1")
        assertTrue(chatState.state.value.isPinned(key))
        assertEquals("Agent 1", chatState.state.value.label(key.token))

        chatState.setMuted(key, true)
        assertTrue(chatState.state.value.isMuted(key))

        chatState.setHidden(key, true)
        assertTrue(chatState.state.value.isHidden(key))

        chatState.unmute(key.token)
        assertFalse(chatState.state.value.isMuted(key))

        chatState.unhide(key.token)
        assertFalse(chatState.state.value.isHidden(key))

        chatState.setPinned(key, false)
        assertFalse(chatState.state.value.isPinned(key))
    }

    @Test
    fun shouldSuppressPushReturnsTrueWhenEitherMutedOrHidden() {
        val storage = InMemoryChatStateStorage()
        val chatState = MobileChatState(storage)
        val key = AgentKey("local", "pane1")

        assertFalse(chatState.shouldSuppressPush(key))

        chatState.setMuted(key, true)
        assertTrue(chatState.shouldSuppressPush(key))

        chatState.setMuted(key, false)
        assertFalse(chatState.shouldSuppressPush(key))

        chatState.setHidden(key, true)
        assertTrue(chatState.shouldSuppressPush(key))

        chatState.setMuted(key, true)
        assertTrue(chatState.shouldSuppressPush(key))
    }

    @Test
    fun markSeenRecordsMaxSeqAndHistoryTimestampAndTransitionsSeenState() {
        val storage = InMemoryChatStateStorage()
        val chatState = MobileChatState(storage)
        val key = AgentKey("local", "pane1")
        val agentSeq10 = testAgent(seq = 10L)

        // Initially unread/unseen
        assertFalse(chatState.state.value.seen(agentSeq10))

        val items = listOf(
            HistoryItem(id = "h1", pane_id = "pane1", query = "q1", response = "r1", completed_at = "2026-10-23T10:00:00Z"),
            HistoryItem(id = "h2", pane_id = "pane1", query = "q2", response = "r2", completed_at = "2026-10-23T10:05:00Z")
        )
        val testNow = 1_700_000_000_000L

        chatState.markSeen(agentSeq10, items, now = testNow)

        // Should now be seen
        assertTrue(chatState.state.value.seen(agentSeq10))
        assertEquals(10L, chatState.state.value.seenSeq(key))
        // Timestamp must be max of history items and now
        assertTrue(chatState.state.value.seenAt(key) >= testNow)

        // When agent sequence advances, chat transitions back to unread
        val agentSeq11 = testAgent(seq = 11L)
        assertFalse(chatState.state.value.seen(agentSeq11))

        // Marking seen again transitions to seen
        chatState.markSeen(agentSeq11, emptyList(), now = testNow + 1_000L)
        assertTrue(chatState.state.value.seen(agentSeq11))
    }

    @Test
    fun pendingForFiltersAcknowledgedAndExpiredTurnsPurelyWithoutSideEffects() {
        val storage = InMemoryChatStateStorage()
        val chatState = MobileChatState(storage)
        val key = AgentKey("local", "pane1")
        val baseTime = 1_700_000_000_000L

        chatState.addOutbox(key, "echo hello", now = baseTime)
        chatState.addOutbox(key, "stale message", now = baseTime - 11 * 60 * 1000L) // 11 mins ago (expired)
        chatState.addOutbox(key, "acknowledged turn", now = baseTime)

        // Snapshot initially has 3 outbox messages
        assertEquals(3, chatState.state.value.outbox[key.token]?.size)

        // History acknowledging "acknowledged turn" (ISO string matching baseTime)
        // 1700000000000L is 2023-11-14T22:13:20Z
        val history = listOf(
            HistoryItem(
                id = "h1",
                pane_id = "pane1",
                query = "acknowledged turn",
                response = "done",
                asked_at = "2023-11-14T22:13:20Z"
            )
        )

        // pendingFor should filter out "stale message" (expired) and "acknowledged turn" (in history)
        val pending = chatState.state.value.pendingFor(key, history, now = baseTime)
        assertEquals(1, pending.size)
        assertEquals("echo hello", pending.first().text)

        // Calling pendingFor is pure: outbox state itself did not mutate
        assertEquals(3, chatState.state.value.outbox[key.token]?.size)

        // reconcileOutbox mutates the state to retain only valid pending messages
        chatState.reconcileOutbox(key, history, now = baseTime)
        assertEquals(1, chatState.state.value.outbox[key.token]?.size)
        assertEquals("echo hello", chatState.state.value.outbox[key.token]?.first()?.text)
    }

    @Test
    fun clearAllResetsFlagsSeenMarksAndOutboxInSingleEmission() {
        val storage = InMemoryChatStateStorage()
        val chatState = MobileChatState(storage)
        val key = AgentKey("local", "pane1")

        chatState.setPinned(key, true, "Agent 1")
        chatState.setMuted(key, true)
        chatState.setHidden(key, true)
        chatState.markRead(key, 5L)
        chatState.addOutbox(key, "pending text")

        val before = chatState.state.value
        assertTrue(before.isPinned(key))
        assertTrue(before.isMuted(key))
        assertTrue(before.isHidden(key))
        assertEquals(5L, before.seenSeq(key))
        assertEquals(1, before.outbox.size)

        chatState.clearAll()

        val after = chatState.state.value
        assertFalse(after.isPinned(key))
        assertFalse(after.isMuted(key))
        assertFalse(after.isHidden(key))
        assertEquals(0, after.pinned.size)
        assertEquals(0, after.muted.size)
        assertEquals(0, after.hidden.size)
        assertEquals(0, after.labels.size)
        assertEquals(0, after.seen.size)
        assertEquals(0, after.outbox.size)

        // Storage is also cleared
        assertEquals(0, storage.loadPinned().size)
        assertEquals(0, storage.loadMuted().size)
        assertEquals(0, storage.loadHidden().size)
        assertEquals(0, storage.loadSeen().size)
    }
}
