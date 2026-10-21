package com.gabriel.agentwatch.mobile

import android.app.Application
import com.gabriel.agentwatch.mobile.push.MobileNotifications

/** Creates the notification channels once per process; the FCM service may start without the activity. */
class MobileApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        MobileNotifications.ensure(this)
    }
}
