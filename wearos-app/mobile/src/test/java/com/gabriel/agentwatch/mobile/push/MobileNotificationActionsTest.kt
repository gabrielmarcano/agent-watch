package com.gabriel.agentwatch.mobile.push

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** The phase 7 lock-screen rule: granting actions need an unlocked phone; the rest do not. */
class MobileNotificationActionsTest {

    @Test
    fun approvingActionsRequireAnUnlock() {
        assertTrue(MobileNotificationActions.requiresUnlock(MobileNotificationActions.ALLOW))
        assertTrue(MobileNotificationActions.requiresUnlock(MobileNotificationActions.ANSWER))
    }

    @Test
    fun denyCancelAndReplyDoNotGrantAnything() {
        assertFalse(MobileNotificationActions.requiresUnlock(MobileNotificationActions.DENY))
        assertFalse(MobileNotificationActions.requiresUnlock(MobileNotificationActions.CANCEL))
        assertFalse(MobileNotificationActions.requiresUnlock(MobileNotificationActions.PROMPT))
        assertFalse(MobileNotificationActions.requiresUnlock(MobileNotificationActions.OPEN))
    }
}
