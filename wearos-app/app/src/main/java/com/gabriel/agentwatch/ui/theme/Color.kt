package com.gabriel.agentwatch.ui.theme

import androidx.annotation.DrawableRes
import androidx.annotation.StringRes
import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.Color
import com.gabriel.agentwatch.R

// Fixed palette (no dynamic colour: status colours carry meaning). Every text pair is ≥ 6.6:1.
internal val Black = Color(0xFF000000)
internal val SurfaceLow = Color(0xFF16181C)
internal val Surface = Color(0xFF1F2227)
internal val SurfaceHigh = Color(0xFF2B2F35)
internal val OnSurface = Color(0xFFF1F3F5)
internal val OnSurfaceVariant = Color(0xFFC3C8CF)
internal val Outline = Color(0xFF8B929A)
internal val OutlineVariant = Color(0xFF454A51)

internal val Blue = Color(0xFF9ECAFF)
internal val OnBlue = Color(0xFF00315B)
internal val BlueContainer = Color(0xFF13304F)
internal val OnBlueContainer = Color(0xFFD3E4FF)

internal val Green = Color(0xFF86DBA5)
internal val OnGreen = Color(0xFF00391F)
internal val GreenContainer = Color(0xFF0E3E26)
internal val OnGreenContainer = Color(0xFFC3F2D5)

internal val Amber = Color(0xFFFFC857)
internal val AmberContainer = Color(0xFF4A3800)
internal val OnAmberContainer = Color(0xFFFFE8B0)

internal val Red = Color(0xFFFFB4AB)
internal val OnRed = Color(0xFF690005)
internal val RedContainer = Color(0xFF7A1A14)
internal val OnRedContainer = Color(0xFFFFDAD6)

internal val Grey = Color(0xFFA5ACB4)

/** How a herdr status looks everywhere: icon, word and colours. The colour is never the only signal. */
@Immutable
data class StatusStyle(
    @DrawableRes val icon: Int,
    @StringRes val label: Int,
    /** Icon and status word on a neutral surface. */
    val accent: Color,
    /** Row container; only `blocked` stands out. */
    val container: Color,
    val onContainer: Color
)

fun statusStyle(status: String): StatusStyle = when (status) {
    "blocked" -> StatusStyle(R.drawable.ic_status_blocked, R.string.status_blocked, Amber, AmberContainer, OnAmberContainer)
    "done" -> StatusStyle(R.drawable.ic_status_done, R.string.status_done, Green, Surface, OnSurface)
    "working" -> StatusStyle(R.drawable.ic_status_working, R.string.status_working, Blue, Surface, OnSurface)
    "idle" -> StatusStyle(R.drawable.ic_status_idle, R.string.status_idle, OnSurfaceVariant, Surface, OnSurface)
    else -> StatusStyle(R.drawable.ic_status_unknown, R.string.status_unknown, Grey, Surface, OnSurface)
}
