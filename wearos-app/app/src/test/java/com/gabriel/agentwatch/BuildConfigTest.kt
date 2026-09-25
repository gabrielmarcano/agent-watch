package com.gabriel.agentwatch

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/** BuildConfig.DEFAULT_RELAY_URL comes from AW_RELAY_DOMAIN in the repo root's agent-watch.env. */
class BuildConfigTest {

    @Test
    fun defaultRelayUrlIsEmptyOrAnHttpsOrigin() {
        val url = BuildConfig.DEFAULT_RELAY_URL
        assertTrue(
            "DEFAULT_RELAY_URL must be \"\" or https://<host>[:port], got \"$url\"",
            url.isEmpty() || Regex("^https://[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$").matches(url),
        )
    }

    @Test
    fun defaultRelayUrlIsEmptyWithoutTheEnvFile() {
        // Unit tests run in wearos-app/app; the file lives at the repo root.
        if (!File("../../agent-watch.env").exists()) {
            assertEquals("", BuildConfig.DEFAULT_RELAY_URL)
        }
    }
}
