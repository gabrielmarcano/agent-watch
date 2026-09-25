package com.gabriel.agentwatch.approval

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
