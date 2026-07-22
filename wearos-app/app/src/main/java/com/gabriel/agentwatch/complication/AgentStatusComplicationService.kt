package com.gabriel.agentwatch.complication

import android.content.Context
import android.graphics.drawable.Icon
import android.util.Log
import androidx.wear.watchface.complications.data.ComplicationData
import androidx.wear.watchface.complications.data.ComplicationType
import androidx.wear.watchface.complications.data.MonochromaticImage
import androidx.wear.watchface.complications.data.PlainComplicationText
import androidx.wear.watchface.complications.data.ShortTextComplicationData
import androidx.wear.watchface.complications.datasource.ComplicationRequest
import androidx.wear.watchface.complications.datasource.SuspendingComplicationDataSourceService
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.model.AgentState
import com.google.gson.Gson
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException

class AgentStatusComplicationService : SuspendingComplicationDataSourceService() {

    private val client = OkHttpClient()
    private val gson = Gson()

    override fun getPreviewData(type: ComplicationType): ComplicationData? {
        if (type != ComplicationType.SHORT_TEXT) return null
        return createComplicationData("Idle", android.R.drawable.ic_dialog_info)
    }

    override suspend fun onComplicationRequest(request: ComplicationRequest): ComplicationData? {
        if (request.complicationType != ComplicationType.SHORT_TEXT) return null

        val state = fetchAgentState()
        
        val text = when (state?.status) {
            "idle", "done" -> "Idle"
            "thinking" -> "Working"
            "waiting_for_permission" -> "Waiting"
            else -> "Offline"
        }

        // We can use different icons based on state if we have them. 
        // For now, we will just use a placeholder text and icon.
        val iconRes = android.R.drawable.ic_dialog_info // Fallback generic icon

        return createComplicationData(text, iconRes)
    }

    private fun createComplicationData(text: String, iconResId: Int): ComplicationData {
        return ShortTextComplicationData.Builder(
            text = PlainComplicationText.Builder(text).build(),
            contentDescription = PlainComplicationText.Builder("Agent Status").build()
        ).setMonochromaticImage(
            MonochromaticImage.Builder(
                Icon.createWithResource(this, iconResId)
            ).build()
        ).build()
    }

    private suspend fun fetchAgentState(): AgentState? = withContext(Dispatchers.IO) {
        val sharedPreferences = getSharedPreferences("AgentWatchPrefs", Context.MODE_PRIVATE)
        val localIp = sharedPreferences.getString("local_ip", "192.168.1.20") ?: return@withContext null
        val url = "http://$localIp:8420/state"

        val request = Request.Builder().url(url).build()
        try {
            client.newCall(request).execute().use { response ->
                if (!response.isSuccessful) return@withContext null
                response.body?.string()?.let {
                    gson.fromJson(it, AgentState::class.java)
                }
            }
        } catch (e: IOException) {
            Log.e("Complication", "Failed to fetch state for complication", e)
            null
        }
    }
}
