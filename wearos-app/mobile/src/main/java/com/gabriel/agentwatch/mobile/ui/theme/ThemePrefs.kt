package com.gabriel.agentwatch.mobile.ui.theme

import android.content.Context
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext

/**
 * The phone's own theme settings: the system palette (dynamic colour, Android 12+) and light or dark.
 * The theme reads them; the Settings screen writes them.
 */
class ThemePrefs(context: Context) {
    private val prefs = context.getSharedPreferences("AgentWatchPhoneTheme", Context.MODE_PRIVATE)
    private val dynamic = mutableStateOf(prefs.getBoolean(KEY_DYNAMIC, true))
    private val mode = mutableStateOf(prefs.getString(KEY_MODE, MODE_SYSTEM) ?: MODE_SYSTEM)

    /** The system palette (the wallpaper's colours) instead of ours. */
    var dynamicColor: Boolean
        get() = dynamic.value
        set(value) {
            dynamic.value = value
            prefs.edit().putBoolean(KEY_DYNAMIC, value).apply()
        }

    /** `system`, `light` or `dark`. */
    var themeMode: String
        get() = mode.value
        set(value) {
            mode.value = value
            prefs.edit().putString(KEY_MODE, value).apply()
        }

    companion object {
        const val MODE_SYSTEM = "system"
        const val MODE_LIGHT = "light"
        const val MODE_DARK = "dark"
        private const val KEY_DYNAMIC = "dynamic_color"
        private const val KEY_MODE = "theme_mode"
    }
}

@Composable
fun rememberThemePrefs(): ThemePrefs {
    val context = LocalContext.current
    return remember(context) { ThemePrefs(context) }
}
