package com.gabriel.agentwatch.approval

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.PendingPrompt
import com.gabriel.agentwatch.model.PromptOption

/**
 * The buttons a `permission` prompt shows on the wrist before "More" is opened.
 *
 * @property allow the option behind the green ALLOW button, or null if none is shown.
 * @property deny the option behind DENY, or null (DENY then falls back to `cancel`).
 * @property hasMore true when some option is only reachable through "More".
 */
data class PermissionButtons(
    val allow: PromptOption?,
    val deny: PromptOption?,
    val hasMore: Boolean
)

/**
 * Picks the primary buttons by role (never by position).
 *
 * The green ALLOW only ever maps to `allow_once`. A prompt without one gets no primary ALLOW,
 * so a single tap can never send "don't ask again" (`allow_always`); that option stays under "More",
 * with its own label.
 */
fun permissionButtons(prompt: PendingPrompt): PermissionButtons {
    val allow = prompt.options.firstOrNull { it.role == "allow_once" }
    val deny = prompt.options.firstOrNull { it.role == "deny" }
    val hasMore = prompt.options.any { it.id != allow?.id && it.id != deny?.id }
    return PermissionButtons(allow, deny, hasMore)
}

/** A command the relay accepted (HTTP 200) for one prompt: the pane plus the seq and fingerprint it was sent with. */
data class SentAnswer(
    val paneId: String,
    val seq: Long,
    val fingerprint: String
)

/**
 * True while [agent] still shows the exact prompt [sent] was accepted for: same pane, still `blocked`,
 * same `state_change_seq` and same prompt fingerprint. The relay has not reported the transition yet,
 * so the prompt's buttons stay disabled; a second tap would re-send with the same seq + fingerprint.
 * Any change (new seq, new fingerprint, prompt gone, agent no longer blocked) unlocks.
 */
fun isAwaitingUpdate(agent: AgentState, sent: SentAnswer?): Boolean {
    if (sent == null) return false
    val prompt = agent.prompt ?: return false
    return agent.pane_id == sent.paneId &&
        agent.status == "blocked" &&
        agent.state_change_seq == sent.seq &&
        prompt.fingerprint == sent.fingerprint
}
