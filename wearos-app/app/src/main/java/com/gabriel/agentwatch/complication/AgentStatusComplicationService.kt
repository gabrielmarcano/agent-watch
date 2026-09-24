package com.gabriel.agentwatch.complication

import android.app.PendingIntent
import android.content.Intent
import android.graphics.drawable.Icon
import android.util.Log
import androidx.wear.watchface.complications.data.ComplicationData
import androidx.wear.watchface.complications.data.ComplicationType
import androidx.wear.watchface.complications.data.MonochromaticImage
import androidx.wear.watchface.complications.data.PlainComplicationText
import androidx.wear.watchface.complications.data.ShortTextComplicationData
import androidx.wear.watchface.complications.datasource.ComplicationRequest
import androidx.wear.watchface.complications.datasource.SuspendingComplicationDataSourceService
import com.gabriel.agentwatch.MainActivity
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.network.RelayClient

class AgentStatusComplicationService : SuspendingComplicationDataSourceService() {

    companion object {
        private const val TAG = "Complication"
    }

    override fun getPreviewData(type: ComplicationType): ComplicationData? {
        if (type != ComplicationType.SHORT_TEXT) return null
        return createComplicationData("✓", "Agent Watch")
    }

    override suspend fun onComplicationRequest(request: ComplicationRequest): ComplicationData? {
        if (request.complicationType != ComplicationType.SHORT_TEXT) return null

        val prefs = Prefs(this)
        if (!prefs.isPaired) {
            return createComplicationData("—", "Not Paired")
        }

        return try {
            val client = RelayClient(prefs.relayUrl, prefs.deviceToken)
            val result = client.agents()

            result.fold(
                onSuccess = { snapshot ->
                    if (!snapshot.host_online) {
                        createComplicationData("—", "Host Offline")
                    } else {
                        val blocked = snapshot.agents.count { it.status == "blocked" }
                        val working = snapshot.agents.count { it.status == "working" }

                        when {
                            blocked > 0 -> createComplicationData("$blocked ⚠", "$blocked Blocked")
                            working > 0 -> createComplicationData("$working ⚙", "$working Working")
                            snapshot.agents.isNotEmpty() -> createComplicationData("✓", "All Done/Idle")
                            else -> createComplicationData("0", "No Agents")
                        }
                    }
                },
                onFailure = { error ->
                    Log.e(TAG, "Complication failed to fetch agents: ${error.message}")
                    createComplicationData("—", "Error")
                }
            )
        } catch (e: Exception) {
            Log.e(TAG, "Complication error", e)
            createComplicationData("—", "Error")
        }
    }

    private fun createComplicationData(text: String, contentDescription: String): ComplicationData {
        val tapIntent = Intent(this, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK
        }
        val pendingIntent = PendingIntent.getActivity(
            this,
            0,
            tapIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        return ShortTextComplicationData.Builder(
            text = PlainComplicationText.Builder(text).build(),
            contentDescription = PlainComplicationText.Builder(contentDescription).build()
        )
            .setTapAction(pendingIntent)
            .setMonochromaticImage(
                MonochromaticImage.Builder(
                    Icon.createWithResource(this, android.R.drawable.ic_dialog_info)
                ).build()
            )
            .build()
    }
}
