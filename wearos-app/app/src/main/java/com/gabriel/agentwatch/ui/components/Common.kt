package com.gabriel.agentwatch.ui.components

import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.wear.compose.foundation.lazy.TransformingLazyColumn
import androidx.wear.compose.foundation.lazy.TransformingLazyColumnItemScope
import androidx.wear.compose.foundation.lazy.TransformingLazyColumnScope
import androidx.wear.compose.foundation.lazy.TransformingLazyColumnState
import androidx.wear.compose.foundation.lazy.rememberTransformingLazyColumnState
import androidx.wear.compose.material3.Icon
import androidx.wear.compose.material3.ScreenScaffold
import androidx.wear.compose.material3.lazy.TransformationSpec
import androidx.wear.compose.material3.lazy.rememberTransformationSpec
import androidx.wear.compose.material3.lazy.transformedHeight
import androidx.compose.ui.res.pluralStringResource
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.ui.logic.Age
import com.gabriel.agentwatch.ui.logic.ageOf
import com.gabriel.agentwatch.ui.logic.shownStatus
import com.gabriel.agentwatch.ui.logic.waitingOnBackground
import com.gabriel.agentwatch.ui.theme.StatusStyle
import com.gabriel.agentwatch.ui.theme.statusStyle
import java.time.Instant

/**
 * How [agent]'s status shows in the list and on its screen: its style and its word. An agent that
 * only waits on its background agents shows as done with their count (`ARCHITECTURE.md` §4b).
 */
@Composable
fun agentStatus(agent: AgentState): Pair<StatusStyle, String> {
    val style = statusStyle(agent.shownStatus())
    val waiting = agent.waitingOnBackground()
    val word = if (waiting > 0) {
        pluralStringResource(R.plurals.status_done_background, waiting, waiting)
    } else {
        stringResource(style.label)
    }
    return style to word
}

/**
 * A scrolling screen: [ScreenScaffold] (the clock moves away as the list scrolls, native rotary input)
 * around a [TransformingLazyColumn] whose items shrink at the round edges. [content] gets the spec for
 * [transformedItem] and the Material components' `transformation`.
 */
@Composable
fun ScreenList(
    state: TransformingLazyColumnState = rememberTransformingLazyColumnState(),
    edgeButton: (@Composable BoxScope.() -> Unit)? = null,
    content: TransformingLazyColumnScope.(TransformationSpec) -> Unit
) {
    val spec = rememberTransformationSpec()
    if (edgeButton != null) {
        ScreenScaffold(scrollState = state, edgeButton = edgeButton) { padding ->
            TransformingLazyColumn(state = state, contentPadding = padding) { content(spec) }
        }
    } else {
        ScreenScaffold(scrollState = state) { padding ->
            TransformingLazyColumn(state = state, contentPadding = padding) { content(spec) }
        }
    }
}

/** Full width, with the height and fade the list applies near the round edges. For non-Material items. */
fun TransformingLazyColumnItemScope.transformedItem(spec: TransformationSpec): Modifier =
    Modifier
        .fillMaxWidth()
        .transformedHeight(this, spec)
        .graphicsLayer {
            with(spec) {
                applyContainerTransformation(scrollProgress)
                applyContentTransformation(scrollProgress)
            }
        }

@Composable
fun ageText(age: Age): String = when (age) {
    Age.JustNow -> stringResource(R.string.age_now)
    is Age.Minutes -> stringResource(R.string.age_minutes, age.n)
    is Age.Hours -> stringResource(R.string.age_hours, age.n)
    is Age.Days -> stringResource(R.string.age_days, age.n)
    Age.Unknown -> ""
}

@Composable
fun ageText(timestamp: String): String = ageText(remember(timestamp) { ageOf(timestamp, Instant.now()) })

@Composable
fun ResIcon(icon: Int, contentDescription: String?, tint: Color, modifier: Modifier = Modifier.size(24.dp)) {
    Icon(painter = painterResource(icon), contentDescription = contentDescription, tint = tint, modifier = modifier)
}
