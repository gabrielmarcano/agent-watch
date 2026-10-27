package com.gabriel.agentwatch.mobile.ui.theme

import android.os.Build
import androidx.annotation.StringRes
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.gabriel.agentwatch.mobile.R

/*
 * The phone's own look: a messenger's palette, light and dark, with the system palette
 * (dynamic colour) on Android 12+ (docs/phases/7-android-ux.md § 9). The watch keeps its
 * fixed dark palette: on a tiny screen its colours carry status meaning. What the phone
 * copies from the watch is the information, not the colours (ARCHITECTURE.md §4b).
 *
 * The status colours are the one exception to dynamic colour: they carry meaning, so they
 * stay ours, in a light and a dark variant, and the status word always travels with them.
 */

// Light palette (a light messenger: a soft canvas, white panels, blue as the accent).
private val LightColors = lightColorScheme(
    primary = Color(0xFF0B57D0),
    onPrimary = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFFD3E3FD),
    onPrimaryContainer = Color(0xFF041E49),
    secondary = Color(0xFF146C2E),
    onSecondary = Color(0xFFFFFFFF),
    secondaryContainer = Color(0xFFB7F2C0),
    onSecondaryContainer = Color(0xFF00210A),
    tertiary = Color(0xFF6B4EA0),
    onTertiary = Color(0xFFFFFFFF),
    tertiaryContainer = Color(0xFFEDDCFF),
    onTertiaryContainer = Color(0xFF250C51),
    background = Color(0xFFF0F2F5),
    onBackground = Color(0xFF111B21),
    surface = Color(0xFFFFFFFF),
    onSurface = Color(0xFF111B21),
    surfaceVariant = Color(0xFFE9EDF2),
    onSurfaceVariant = Color(0xFF5F6B72),
    surfaceContainer = Color(0xFFF7F8FA),
    surfaceContainerHigh = Color(0xFFE9EDF2),
    outline = Color(0xFFB4BCC2),
    outlineVariant = Color(0xFFDDE2E6),
    error = Color(0xFFB3261E),
    onError = Color(0xFFFFFFFF),
    errorContainer = Color(0xFFF9DEDC),
    onErrorContainer = Color(0xFF410E0B)
)

// Dark palette (the dark messenger: a near-black canvas, panels a shade lighter).
private val DarkColors = darkColorScheme(
    primary = Color(0xFF53BDEB),
    onPrimary = Color(0xFF00363F),
    primaryContainer = Color(0xFF12454F),
    onPrimaryContainer = Color(0xFFC7E9F5),
    secondary = Color(0xFF86DBA5),
    onSecondary = Color(0xFF00391F),
    secondaryContainer = Color(0xFF0E3E26),
    onSecondaryContainer = Color(0xFFC3F2D5),
    tertiary = Color(0xFFD3BBFF),
    onTertiary = Color(0xFF3B1B66),
    tertiaryContainer = Color(0xFF523183),
    onTertiaryContainer = Color(0xFFEBDDFF),
    background = Color(0xFF0B141A),
    onBackground = Color(0xFFE9EDEF),
    surface = Color(0xFF111B21),
    onSurface = Color(0xFFE9EDEF),
    surfaceVariant = Color(0xFF202C33),
    onSurfaceVariant = Color(0xFF8696A0),
    surfaceContainer = Color(0xFF162127),
    surfaceContainerHigh = Color(0xFF202C33),
    outline = Color(0xFF6B7A83),
    outlineVariant = Color(0xFF2A3942),
    error = Color(0xFFF2B8B5),
    onError = Color(0xFF601410),
    errorContainer = Color(0xFF8C1D18),
    onErrorContainer = Color(0xFFF9DEDC)
)

/** Whether the app is in its dark scheme. The status colours pick their variant with it. */
val LocalIsDarkTheme = staticCompositionLocalOf { true }

/** The phone's own theme settings (dynamic colour, light or dark). The Settings screen writes them. */
val LocalThemePrefs = staticCompositionLocalOf<ThemePrefs> {
    error("ThemePrefs not provided; wrap the app in AgentWatchMobileTheme")
}

/** Rounder than the default: a messenger's shapes, with roomy pills and cards. */
private val AgentWatchShapes = Shapes(
    extraSmall = RoundedCornerShape(8.dp),
    small = RoundedCornerShape(12.dp),
    medium = RoundedCornerShape(16.dp),
    large = RoundedCornerShape(20.dp),
    extraLarge = RoundedCornerShape(28.dp)
)

/** The default Material 3 type scale, with the body a touch larger: the phone is read at arm's length. */
private val AgentWatchTypography = Typography().let { base ->
    base.copy(
        bodyLarge = base.bodyLarge.copy(fontSize = 17.sp, lineHeight = 24.sp),
        bodyMedium = base.bodyMedium.copy(fontSize = 15.sp, lineHeight = 21.sp),
        labelLarge = base.labelLarge.copy(fontSize = 14.sp, lineHeight = 20.sp)
    )
}

/**
 * The phone's theme: the system palette when the owner wants it (Android 12+), ours otherwise,
 * following the system's light/dark setting.
 *
 * The expressive motion scheme of Material 3 is still internal in Material 3 1.4.0; it arrives with
 * the AGP 9 / Compose 1.12 change (docs/phases/7-android-ux.md § 1).
 */
@Composable
fun AgentWatchMobileTheme(
    themePrefs: ThemePrefs = rememberThemePrefs(),
    content: @Composable () -> Unit
) {
    val dark = when (themePrefs.themeMode) {
        ThemePrefs.MODE_LIGHT -> false
        ThemePrefs.MODE_DARK -> true
        else -> isSystemInDarkTheme()
    }
    val context = LocalContext.current
    val scheme: ColorScheme = when {
        themePrefs.dynamicColor && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S ->
            if (dark) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
        dark -> DarkColors
        else -> LightColors
    }
    CompositionLocalProvider(
        LocalIsDarkTheme provides dark,
        LocalThemePrefs provides themePrefs
    ) {
        MaterialTheme(
            colorScheme = scheme,
            shapes = AgentWatchShapes,
            typography = AgentWatchTypography,
            content = content
        )
    }
}

/**
 * How a herdr status looks everywhere: word and colours. The colour is never the only signal, and
 * the word doubles as the colour's content description.
 */
@Immutable
data class StatusStyle(
    @StringRes val label: Int,
    /** The accent for the status dot. */
    val accent: Color,
    /** The row container; only `blocked` stands out. */
    val container: Color
)

@Composable
fun statusStyle(status: String): StatusStyle {
    val dark = LocalIsDarkTheme.current
    return when (status) {
        "blocked" -> StatusStyle(
            R.string.status_blocked,
            accent = if (dark) Color(0xFFFFC857) else Color(0xFF8A5000),
            container = if (dark) Color(0xFF4A3800) else Color(0xFFFFE8B0)
        )
        "done" -> StatusStyle(
            R.string.status_done,
            accent = if (dark) Color(0xFF86DBA5) else Color(0xFF146C2E),
            container = Color.Transparent
        )
        "working" -> StatusStyle(
            R.string.status_working,
            accent = if (dark) Color(0xFF9ECAFF) else Color(0xFF0B57D0),
            container = Color.Transparent
        )
        "idle" -> StatusStyle(
            R.string.status_idle,
            accent = if (dark) Color(0xFFA5ACB4) else Color(0xFF5F6B72),
            container = Color.Transparent
        )
        else -> StatusStyle(
            R.string.status_unknown,
            accent = if (dark) Color(0xFFA5ACB4) else Color(0xFF5F6B72),
            container = Color.Transparent
        )
    }
}

/** The warning accent (the blocked status colour): for the states that need attention. */
@Composable
fun warningAccent(): Color = statusStyle("blocked").accent
