package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.network.NotificationActionReceiver.Companion.ACTION_ANSWER
import com.gabriel.agentwatch.network.NotificationActionReceiver.Companion.ACTION_CANCEL
import com.gabriel.agentwatch.network.NotificationActionReceiver.Companion.ACTION_PROMPT
import com.gabriel.agentwatch.network.NotificationActionReceiver.Companion.isBlankReply
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class NotificationActionReceiverTest {

    // A Reply without text gets an error line on the notification instead of vanishing.
    @Test
    fun replyWithoutTextIsBlank() {
        assertTrue(isBlankReply(ACTION_PROMPT, null))
        assertTrue(isBlankReply(ACTION_PROMPT, ""))
        assertTrue(isBlankReply(ACTION_PROMPT, "  \n"))
    }

    @Test
    fun replyWithTextIsSent() {
        assertFalse(isBlankReply(ACTION_PROMPT, "run the tests"))
    }

    // Answers and cancels carry no text by design.
    @Test
    fun otherActionsNeedNoText() {
        assertFalse(isBlankReply(ACTION_ANSWER, null))
        assertFalse(isBlankReply(ACTION_CANCEL, null))
    }
}
