package com.gabriel.agentwatch.mobile

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.util.Log
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.gabriel.agentwatch.mobile.push.MobileNotifications
import com.gabriel.agentwatch.mobile.push.MobilePush
import com.gabriel.agentwatch.mobile.ui.MobileApp
import com.gabriel.agentwatch.mobile.ui.theme.AgentWatchMobileTheme
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.network.RelayRepository

/** The phone client: a first-class relay client next to the watch (AGENTS.md §1.6). */
class MainActivity : ComponentActivity() {
    private var pendingDeepLink by mutableStateOf<AgentKey?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // The app draws behind the system bars (targetSdk 36). The phone follows the system's
        // light/dark setting, so the bars pick their icons the same way
        // (docs/phases/7-android-ux.md § 1).
        enableEdgeToEdge()
        pendingDeepLink = MobileNotifications.agentOf(intent)

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
                Log.d("MainActivity", "Notification permission granted: $granted")
            }.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        MobilePush.fetchToken(this)

        setContent {
            AgentWatchMobileTheme {
                MobileApp(
                    deepLink = pendingDeepLink,
                    onDeepLinkHandled = { pendingDeepLink = null }
                )
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        MobileNotifications.agentOf(intent)?.let { pendingDeepLink = it }
    }

    // The SSE stream lives only while the app is in the foreground (battery); push arrives
    // through FCM, so the chat list is fresh again on the next start.
    override fun onStart() {
        super.onStart()
        RelayRepository.start(this)
        MobilePush.ensure(this)
    }

    override fun onStop() {
        super.onStop()
        RelayRepository.stop()
    }
}
