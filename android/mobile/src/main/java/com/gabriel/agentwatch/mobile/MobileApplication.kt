package com.gabriel.agentwatch.mobile

import android.app.Application
import android.content.Context
import com.gabriel.agentwatch.mobile.push.MobileNotifications
import com.gabriel.agentwatch.mobile.ui.MobileChatState
import com.gabriel.agentwatch.mobile.ui.SharedPrefsChatStateStorage
import com.gabriel.agentwatch.network.AgentsUpdate
import com.gabriel.agentwatch.network.RelayObservers
import com.gabriel.agentwatch.network.RelayRepository

/** Creates the notification channels once per process; the FCM service may start without the activity. */
class MobileApplication : Application() {
    lateinit var chatState: MobileChatState
        private set

    override fun onCreate() {
        super.onCreate()
        instance = this
        chatState = testChatState ?: MobileChatState(SharedPrefsChatStateStorage(this))

        RelayRepository.observers = object : RelayObservers {
            override fun onAgentsUpdated(context: Context, update: AgentsUpdate) {
                MobileNotifications.dismissStale(context, update)
            }

            override fun onAuthCleared(context: Context) {
                MobileNotifications.cancelAll(context)
                chatState.clearAll()
            }
        }

        MobileNotifications.ensure(this)
    }

    companion object {
        private var instance: MobileApplication? = null

        fun get(): MobileApplication =
            instance ?: error("MobileApplication not initialized")

        var testChatState: MobileChatState? = null

        fun chatState(context: Context): MobileChatState =
            testChatState ?: (context.applicationContext as? MobileApplication)?.chatState
                ?: MobileChatState(SharedPrefsChatStateStorage(context))
    }
}
