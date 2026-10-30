package com.gabriel.agentwatch.ui.theme

import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.wear.compose.material3.ColorScheme
import androidx.wear.compose.material3.MaterialTheme

private val AgentWatchColors = ColorScheme(
    primary = Blue,
    primaryDim = Color(0xFF7FA9D9),
    primaryContainer = BlueContainer,
    onPrimary = OnBlue,
    onPrimaryContainer = OnBlueContainer,
    secondary = Color(0xFFBFC8D6),
    secondaryDim = Color(0xFFA3ACBA),
    secondaryContainer = Color(0xFF353D49),
    onSecondary = Color(0xFF29313D),
    onSecondaryContainer = Color(0xFFDBE3F1),
    tertiary = Green,
    tertiaryDim = Color(0xFF6BBF8A),
    tertiaryContainer = GreenContainer,
    onTertiary = OnGreen,
    onTertiaryContainer = OnGreenContainer,
    surfaceContainerLow = SurfaceLow,
    surfaceContainer = Surface,
    surfaceContainerHigh = SurfaceHigh,
    onSurface = OnSurface,
    onSurfaceVariant = OnSurfaceVariant,
    outline = Outline,
    outlineVariant = OutlineVariant,
    background = Black,
    onBackground = OnSurface,
    error = Red,
    errorDim = Color(0xFFE59A91),
    errorContainer = RedContainer,
    onError = OnRed,
    onErrorContainer = OnRedContainer,
)

/** Wear Compose Material 3 with the app's fixed palette and the default type scale (never below 12 sp in use). */
@Composable
fun AgentWatchTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = AgentWatchColors, content = content)
}
