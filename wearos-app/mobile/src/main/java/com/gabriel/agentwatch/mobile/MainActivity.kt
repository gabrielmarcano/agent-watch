package com.gabriel.agentwatch.mobile

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import com.gabriel.agentwatch.mobile.ui.MobileApp
import com.gabriel.agentwatch.mobile.ui.theme.AgentWatchMobileTheme
import com.gabriel.agentwatch.network.RelayRepository

/** The phone client: a first-class relay client next to the watch (AGENTS.md §1.6). */
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            AgentWatchMobileTheme {
                MobileApp()
            }
        }
    }

    // The SSE stream lives only while the app is in the foreground (battery). The phone app has no
    // push notification of its own yet, so there is nothing to keep alive in the background.
    override fun onStart() {
        super.onStart()
        RelayRepository.start(this)
    }

    override fun onStop() {
        super.onStop()
        RelayRepository.stop()
    }
}
