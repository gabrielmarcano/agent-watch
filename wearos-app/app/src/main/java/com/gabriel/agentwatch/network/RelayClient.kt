package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.*
import com.google.gson.Gson
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.sse.EventSource
import okhttp3.sse.EventSourceListener
import okhttp3.sse.EventSources
import java.net.URLEncoder
import java.util.concurrent.TimeUnit

class RelayError(
    val code: String,
    override val message: String,
    val httpStatus: Int
) : Exception("[$code] $message (HTTP $httpStatus)")

class RelayClient(
    private val baseUrl: String,
    private val token: String? = null
) {
    private val gson = Gson()
    private val jsonMediaType = "application/json; charset=utf-8".toMediaType()

    private val httpClient = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .writeTimeout(10, TimeUnit.SECONDS)
        .build()

    private val sseHttpClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.MILLISECONDS) // SSE connections must not time out
        .retryOnConnectionFailure(true)
        .build()

    private fun cleanBaseUrl(): String {
        return baseUrl.trim().trimEnd('/')
    }

    private fun newRequestBuilder(path: String): Request.Builder {
        val url = if (path.startsWith("http://") || path.startsWith("https://")) path else "${cleanBaseUrl()}$path"
        val builder = Request.Builder().url(url)
        if (!token.isNullOrBlank()) {
            builder.header("Authorization", "Bearer $token")
        }
        return builder
    }

    private fun parseError(response: Response, bodyStr: String?): RelayError {
        val status = response.code
        if (!bodyStr.isNullOrBlank()) {
            try {
                val errResp = gson.fromJson(bodyStr, ErrorResponse::class.java)
                if (errResp != null && errResp.error.code.isNotBlank()) {
                    return RelayError(errResp.error.code, errResp.error.message, status)
                }
            } catch (_: Exception) {}
        }
        return RelayError("http_$status", response.message.ifBlank { "HTTP Error $status" }, status)
    }

    private suspend fun <T> executeRequest(request: Request, classOfT: Class<T>): Result<T> = withContext(Dispatchers.IO) {
        try {
            httpClient.newCall(request).execute().use { response ->
                val bodyStr = response.body?.string()
                if (!response.isSuccessful) {
                    return@withContext Result.failure(parseError(response, bodyStr))
                }
                if (classOfT == Unit::class.java) {
                    @Suppress("UNCHECKED_CAST")
                    return@withContext Result.success(Unit as T)
                }
                if (bodyStr.isNullOrBlank()) {
                    return@withContext Result.failure(RelayError("empty_response", "Empty response from relay", response.code))
                }
                val parsed = gson.fromJson(bodyStr, classOfT)
                Result.success(parsed)
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    suspend fun pair(code: String, deviceName: String): Result<PairResponse> {
        val reqBody = gson.toJson(PairRequest(code = code.trim(), device_name = deviceName)).toRequestBody(jsonMediaType)
        val request = newRequestBuilder("/v1/pair").post(reqBody).build()
        return executeRequest(request, PairResponse::class.java)
    }

    suspend fun agents(): Result<AgentsSnapshot> {
        val request = newRequestBuilder("/v1/agents").get().build()
        return executeRequest(request, AgentsSnapshot::class.java)
    }

    suspend fun history(paneId: String? = null, limit: Int = 20): Result<List<HistoryItem>> {
        var path = "/v1/history?limit=$limit"
        if (!paneId.isNullOrBlank()) {
            path += "&pane_id=${URLEncoder.encode(paneId, "UTF-8")}"
        }
        val request = newRequestBuilder(path).get().build()
        val res = executeRequest(request, HistoryResponse::class.java)
        return res.map { it.items }
    }

    suspend fun prompt(paneId: String, text: String, expectedSeq: Long): Result<Unit> {
        val encodedPane = URLEncoder.encode(paneId, "UTF-8")
        val reqBody = gson.toJson(PromptRequest(text = text, expected_seq = expectedSeq)).toRequestBody(jsonMediaType)
        val request = newRequestBuilder("/v1/agents/$encodedPane/prompt").post(reqBody).build()
        return executeRequest(request, Unit::class.java)
    }

    suspend fun answer(paneId: String, optionId: String, expectedSeq: Long, fingerprint: String): Result<Unit> {
        val encodedPane = URLEncoder.encode(paneId, "UTF-8")
        val reqBody = gson.toJson(AnswerRequest(option_id = optionId, expected_seq = expectedSeq, fingerprint = fingerprint)).toRequestBody(jsonMediaType)
        val request = newRequestBuilder("/v1/agents/$encodedPane/answer").post(reqBody).build()
        return executeRequest(request, Unit::class.java)
    }

    suspend fun cancel(paneId: String, expectedSeq: Long): Result<Unit> {
        val encodedPane = URLEncoder.encode(paneId, "UTF-8")
        val reqBody = gson.toJson(CancelRequest(expected_seq = expectedSeq)).toRequestBody(jsonMediaType)
        val request = newRequestBuilder("/v1/agents/$encodedPane/cancel").post(reqBody).build()
        return executeRequest(request, Unit::class.java)
    }

    suspend fun registerPush(fcmToken: String): Result<Unit> {
        val reqBody = gson.toJson(PushRegisterRequest(platform = "fcm", token = fcmToken)).toRequestBody(jsonMediaType)
        val request = newRequestBuilder("/v1/push/register").post(reqBody).build()
        return executeRequest(request, Unit::class.java)
    }

    fun events(listener: EventSourceListener): EventSource {
        val request = newRequestBuilder("/v1/events")
            .header("Accept", "text/event-stream")
            .get()
            .build()
        return EventSources.createFactory(sseHttpClient).newEventSource(request, listener)
    }
}
