package com.gabriel.agentwatch.mobile.ui.theme

import com.gabriel.agentwatch.data.InMemoryKeyValueStore
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ThemePrefsTest {

    @Test
    fun defaultsToDynamicColorAndSystemMode() {
        val store = InMemoryKeyValueStore()
        val themePrefs = ThemePrefs(store)

        assertTrue(themePrefs.dynamicColor)
        assertEquals(ThemePrefs.MODE_SYSTEM, themePrefs.themeMode)
    }

    @Test
    fun updatesStoreAndProperties() {
        val store = InMemoryKeyValueStore()
        val themePrefs = ThemePrefs(store)

        themePrefs.dynamicColor = false
        themePrefs.themeMode = ThemePrefs.MODE_DARK

        assertFalse(themePrefs.dynamicColor)
        assertEquals(ThemePrefs.MODE_DARK, themePrefs.themeMode)
        assertFalse(store.getBoolean("dynamic_color", true))
        assertEquals(ThemePrefs.MODE_DARK, store.getString("theme_mode"))
    }
}
