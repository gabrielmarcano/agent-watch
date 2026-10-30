package com.gabriel.agentwatch.mobile.ui

import androidx.compose.animation.AnimatedVisibilityScope
import androidx.compose.animation.SharedTransitionScope
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier

/**
 * The agent's face, shared between the chat list and the conversation: the avatar that flies from
 * the row into the chat's top bar (the shared transition of Compose).
 *
 * Without the scopes (the app's previews, or a screen outside the transition) it is no modifier at
 * all, and the avatar is simply drawn where it is.
 */
@Composable
fun sharedFace(
    token: String,
    sharedScope: SharedTransitionScope?,
    visibilityScope: AnimatedVisibilityScope?
): Modifier {
    if (sharedScope == null || visibilityScope == null) return Modifier
    return with(sharedScope) {
        Modifier.sharedElement(
            rememberSharedContentState("avatar-$token"),
            animatedVisibilityScope = visibilityScope
        )
    }
}
