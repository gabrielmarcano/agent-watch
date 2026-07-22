package com.gabriel.agentwatch.network

import android.util.Log
import com.gabriel.agentwatch.model.AgentState
import com.google.gson.Gson
import com.google.firebase.messaging.FirebaseMessaging
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.sse.EventSource
import okhttp3.sse.EventSourceListener
import okhttp3.sse.EventSources
import java.io.IOException
import java.util.concurrent.TimeUnit

class SseClient(
    private val localIp: String,
    private val tailscaleIp: String,
    private val port: Int = 8420
) {
    private val client = OkHttpClient.Builder()
        .connectTimeout(3, TimeUnit.SECONDS) // Lower timeout for faster fallback
        .readTimeout(10, TimeUnit.MINUTES)
        .build()

    private val gson = Gson()
    private var eventSource: EventSource? = null
    
    private var currentIp = localIp
    private var tryingFallback = false

    private val _stateFlow = MutableStateFlow(AgentState())
    val stateFlow: StateFlow<AgentState> = _stateFlow

    fun startListening() {
        if (eventSource != null) return

        val url = "http://$currentIp:$port/events"
        Log.d("SseClient", "Connecting to SSE: $url")
        val request = Request.Builder()
            .url(url)
            .build()

        val listener = object : EventSourceListener() {
            override fun onOpen(eventSource: EventSource, response: Response) {
                Log.d("SseClient", "SSE Connection Opened to $currentIp")
                tryingFallback = false
                
                try {
                    FirebaseMessaging.getInstance().token.addOnCompleteListener { task ->
                        if (task.isSuccessful) {
                            val token = task.result
                            Log.d("SseClient", "FCM token fetched for registration: ${token?.take(15)}...")
                            if (token != null) {
                                registerFcmToken(token)
                            }
                        } else {
                            Log.e("SseClient", "Failed to fetch FCM token", task.exception)
                        }
                    }
                } catch (e: Exception) {
                    Log.e("SseClient", "Error fetching FCM token", e)
                }
            }

            override fun onEvent(
                eventSource: EventSource,
                id: String?,
                type: String?,
                data: String
            ) {
                Log.d("SseClient", "Event from $currentIp: type=$type")
                if (type == "state") {
                    try {
                        val state = gson.fromJson(data, AgentState::class.java)
                        _stateFlow.value = state
                    } catch (e: Exception) {
                        Log.e("SseClient", "Error parsing SSE state JSON", e)
                    }
                }
            }

            override fun onClosed(eventSource: EventSource) {
                Log.d("SseClient", "SSE Connection Closed for $currentIp")
                reconnect()
            }

            override fun onFailure(eventSource: EventSource, t: Throwable?, response: Response?) {
                Log.e("SseClient", "SSE Connection Failed for $currentIp: ${t?.message}")
                
                if (currentIp == localIp && !tryingFallback) {
                    Log.d("SseClient", "Switching to Tailscale fallback: $tailscaleIp")
                    currentIp = tailscaleIp
                    tryingFallback = true
                    stopListening()
                    startListening()
                } else {
                    Log.d("SseClient", "Connection failed for both. Retrying local in 3s")
                    currentIp = localIp
                    tryingFallback = false
                    reconnect()
                }
            }
        }

        eventSource = EventSources.createFactory(client).newEventSource(request, listener)
    }

    fun stopListening() {
        eventSource?.cancel()
        eventSource = null
    }

    private fun reconnect() {
        stopListening()
        CoroutineScope(Dispatchers.IO).launch {
            delay(3000)
            startListening()
        }
    }

    fun sendInputCommand(text: String, onResult: (Boolean) -> Unit = {}) {
        CoroutineScope(Dispatchers.IO).launch {
            val url = "http://$currentIp:$port/input"
            val json = gson.toJson(mapOf("text" to text))
            val body = json.toRequestBody("application/json".toMediaType())
            
            val request = Request.Builder()
                .url(url)
                .post(body)
                .build()

            try {
                client.newCall(request).execute().use { response ->
                    onResult(response.isSuccessful)
                }
            } catch (e: IOException) {
                Log.e("SseClient", "Failed to send input to $currentIp", e)
                onResult(false)
            }
        }
    }

    fun registerFcmToken(token: String) {
        CoroutineScope(Dispatchers.IO).launch {
            val url = "http://$currentIp:$port/register"
            val json = gson.toJson(mapOf("token" to token))
            val body = json.toRequestBody("application/json".toMediaType())
            
            val request = Request.Builder()
                .url(url)
                .post(body)
                .build()

            try {
                client.newCall(request).execute().use { response ->
                    Log.d("SseClient", "Register FCM token response at $currentIp: ${response.isSuccessful}")
                }
            } catch (e: IOException) {
                Log.e("SseClient", "Failed to register FCM token at $currentIp", e)
            }
        }
    }
}
