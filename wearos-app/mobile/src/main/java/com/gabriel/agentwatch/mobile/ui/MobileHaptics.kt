package com.gabriel.agentwatch.mobile.ui

import android.view.HapticFeedbackConstants
import android.view.View
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.interaction.InteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalView

/**
 * Tactile haptics tuned for Android 14+ (Pixel 8 Pro linear actuator) with minSdk 30 fallbacks.
 * Each user gesture gets a distinct, quiet physical signature:
 * - [tick]: ultra-light crisp tick for filter chips, tab switches, scroll-to-bottom and row taps.
 * - [send]: firm release pop when sending a message or starting a session.
 * - [confirm]: double-pulse confirmation when approving a permission or answering a question.
 * - [reject]: sharp dampened thud when denying or cancelling a prompt.
 * - [gestureThreshold]: crisp snap when a swipe-to-read crosses its activation threshold.
 */
object MobileHaptics {
    fun tick(view: View) {
        view.performHapticFeedback(HapticFeedbackConstants.CLOCK_TICK)
    }

    fun send(view: View) {
        view.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
    }

    fun confirm(view: View) {
        view.performHapticFeedback(HapticFeedbackConstants.CONFIRM)
    }

    fun reject(view: View) {
        view.performHapticFeedback(HapticFeedbackConstants.REJECT)
    }

    fun gestureThreshold(view: View) {
        val constant = if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            HapticFeedbackConstants.GESTURE_THRESHOLD_ACTIVATE
        } else {
            HapticFeedbackConstants.CLOCK_TICK
        }
        view.performHapticFeedback(constant)
    }

    fun longPress(view: View) {
        view.performHapticFeedback(HapticFeedbackConstants.LONG_PRESS)
    }
}

@Composable
fun rememberHapticView(): View = LocalView.current

/**
 * Scales a button or pill down slightly while pressed and springs back on release, giving buttons
 * WhatsApp's responsive physical squish.
 */
fun Modifier.pressScale(
    interactionSource: InteractionSource,
    pressedScale: Float = 0.92f
): Modifier = composed {
    val pressed by interactionSource.collectIsPressedAsState()
    val scale by animateFloatAsState(
        targetValue = if (pressed) pressedScale else 1f,
        animationSpec = spring(
            dampingRatio = Spring.DampingRatioMediumBouncy,
            stiffness = Spring.StiffnessMediumLow
        ),
        label = "pressScale"
    )
    this.graphicsLayer {
        scaleX = scale
        scaleY = scale
    }
}
