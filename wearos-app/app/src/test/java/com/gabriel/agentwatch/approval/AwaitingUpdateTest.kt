package com.gabriel.agentwatch.approval

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.PendingPrompt
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AwaitingUpdateTest {

    private fun blocked(seq: Long = 334, fingerprint: String = "9f2c61d0a4b3e871", pane: String = "w5:pAE") = AgentState(
        pane_id = pane,
        status = "blocked",
        state_change_seq = seq,
        prompt = PendingPrompt(kind = "permission", title = "Bash command", fingerprint = fingerprint)
    )

    private val sent = SentAnswer(paneId = "w5:pAE", seq = 334, fingerprint = "9f2c61d0a4b3e871")

    @Test
    fun samePromptAfterASuccessfulSendStaysLocked() {
        assertTrue("a second tap would re-send with the same seq + fingerprint", isAwaitingUpdate(blocked(), sent))
    }

    @Test
    fun nothingSentMeansNotLocked() {
        assertFalse(isAwaitingUpdate(blocked(), null))
    }

    @Test
    fun newSeqUnlocks() {
        assertFalse(isAwaitingUpdate(blocked(seq = 335), sent))
    }

    @Test
    fun newFingerprintUnlocks() {
        assertFalse(isAwaitingUpdate(blocked(fingerprint = "0000000000000000"), sent))
    }

    @Test
    fun sameFingerprintWithNewSeqIsANewPromptAndUnlocks() {
        // e.g. the agent asks for the exact same command again after the first answer.
        assertFalse(isAwaitingUpdate(blocked(seq = 336), sent))
    }

    @Test
    fun leavingBlockedUnlocks() {
        assertFalse(isAwaitingUpdate(blocked().copy(status = "working", prompt = null), sent))
        assertFalse(isAwaitingUpdate(blocked().copy(status = "done"), sent))
    }

    @Test
    fun promptGoneUnlocks() {
        assertFalse(isAwaitingUpdate(blocked().copy(prompt = null), sent))
    }

    @Test
    fun otherPaneIsNotLocked() {
        assertFalse(isAwaitingUpdate(blocked(pane = "w5:pZZ"), sent))
    }

    @Test
    fun cancelOnUnknownPromptWithEmptyFingerprintStaysLocked() {
        assertTrue(isAwaitingUpdate(blocked(fingerprint = ""), SentAnswer("w5:pAE", 334, "")))
    }
}
