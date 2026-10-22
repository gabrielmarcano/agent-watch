package com.gabriel.agentwatch.mobile.push

/**
 * The phone notification actions, in one place so the builders, the receiver and the tests agree.
 * [requiresUnlock] is the security rule of phase 7: the actions that grant something (Allow, a
 * question's answer) need an unlocked phone; Deny, Cancel and a reply do not grant anything.
 */
object MobileNotificationActions {
    const val ALLOW = "com.gabriel.agentwatch.mobile.ACTION_ALLOW"
    const val DENY = "com.gabriel.agentwatch.mobile.ACTION_DENY"
    const val ANSWER = "com.gabriel.agentwatch.mobile.ACTION_ANSWER"
    const val CANCEL = "com.gabriel.agentwatch.mobile.ACTION_CANCEL"
    const val PROMPT = "com.gabriel.agentwatch.mobile.ACTION_PROMPT"
    /** The tap that opens the chat; it carries no command, so the receiver never handles it. */
    const val OPEN = "com.gabriel.agentwatch.mobile.ACTION_OPEN"
    /** A chat notification was dismissed: recompute the group summary. */
    const val REFRESH = "com.gabriel.agentwatch.mobile.ACTION_REFRESH"
    /** The group summary was dismissed: take its chats with it. */
    const val DISMISS_ALL = "com.gabriel.agentwatch.mobile.ACTION_DISMISS_ALL"

    fun requiresUnlock(action: String): Boolean = action == ALLOW || action == ANSWER
}
