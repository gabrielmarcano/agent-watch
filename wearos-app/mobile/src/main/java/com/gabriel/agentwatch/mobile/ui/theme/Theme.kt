package com.gabriel.agentwatch.mobile.ui.theme

import androidx.annotation.StringRes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.Color
import com.gabriel.agentwatch.mobile.R

/*
 * The product's fixed palette, mirrored from the watch app's ui/theme/Color.kt: status colour carries
 * meaning, so no client uses dynamic colour, and the phone keeps the same dark look. Other clients
 * copy the watch's look (wearos-app/ARCHITECTURE.md §4b).
 */
val Black = Color(0xFF000000)
val SurfaceLow = Color(0xFF16181C)
val Surface = Color(0xFF1F2227)
val SurfaceHigh = Color(0xFF2B2F35)
val OnSurface = Color(0xFFF1F3F5)
val OnSurfaceVariant = Color(0xFFC3C8CF)
val Outline = Color(0xFF8B929A)
val OutlineVariant = Color(0xFF454A51)

val Blue = Color(0xFF9ECAFF)
val Green = Color(0xFF86DBA5)
val Amber = Color(0xFFFFC857)
val AmberContainer = Color(0xFF4A3800)
val OnAmberContainer = Color(0xFFFFE8B0)
val Red = Color(0xFFFFB4AB)
val Grey = Color(0xFFA5ACB4)

private val DarkColors = darkColorScheme(
    primary = Blue,
    onPrimary = Color(0xFF00315B),
    primaryContainer = Color(0xFF13304F),
    onPrimaryContainer = Color(0xFFD3E4FF),
    secondary = Color(0xFFBFC8D6),
    onSecondary = Color(0xFF29313D),
    secondaryContainer = Color(0xFF353D49),
    onSecondaryContainer = Color(0xFFDBE3F1),
    tertiary = Green,
    onTertiary = Color(0xFF00391F),
    tertiaryContainer = Color(0xFF0E3E26),
    onTertiaryContainer = Color(0xFFC3F2D5),
    background = Black,
    onBackground = OnSurface,
    surface = Surface,
    onSurface = OnSurface,
    surfaceVariant = SurfaceHigh,
    onSurfaceVariant = OnSurfaceVariant,
    outline = Outline,
    outlineVariant = OutlineVariant,
    error = Red,
    onError = Color(0xFF690005),
    errorContainer = Color(0xFF7A1A14),
    onErrorContainer = Color(0xFFFFDAD6),
)

/** Compose Material 3 for phones with the fixed palette. */
@Composable
fun AgentWatchMobileTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = DarkColors, content = content)
}

/**
 * How a herdr status looks everywhere: word and colours. The colour is never the only signal, and the
 * word doubles as the colour's content description.
 */
@Immutable
data class StatusStyle(
    @StringRes val label: Int,
    /** The accent for the status dot. */
    val accent: Color,
    /** The row container; only `blocked` stands out. */
    val container: Color
)

fun statusStyle(status: String): StatusStyle = when (status) {
    "blocked" -> StatusStyle(R.string.status_blocked, Amber, AmberContainer)
    "done" -> StatusStyle(R.string.status_done, Green, Surface)
    "working" -> StatusStyle(R.string.status_working, Blue, Surface)
    "idle" -> StatusStyle(R.string.status_idle, OnSurfaceVariant, Surface)
    else -> StatusStyle(R.string.status_unknown, Grey, Surface)
}
