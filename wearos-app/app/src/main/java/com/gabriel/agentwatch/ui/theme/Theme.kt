package com.gabriel.agentwatch.ui.theme

import androidx.compose.runtime.Composable
import androidx.wear.compose.material.Colors
import androidx.wear.compose.material.MaterialTheme

private val WearAppColors = Colors(
    primary = Purple200,
    primaryVariant = Purple500,
    secondary = Teal200,
    error = Red400,
    onPrimary = Gray900,
    onSecondary = Gray900,
    onError = Gray900
)

@Composable
fun AgentWatchTheme(
    content: @Composable () -> Unit
) {
    MaterialTheme(
        colors = WearAppColors,
        content = content
    )
}
