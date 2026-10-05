package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.*
import com.google.gson.Gson
import kotlinx.coroutines.suspendCancellableCoroutine
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.sse.EventSource
import okhttp3.sse.EventSourceListener
import okhttp3.sse.EventSources
import java.io.IOException
import java.io.InterruptedIOException
import java.net.SocketTimeoutException
import java.net.URLEncoder
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume

class RelayError(
    val code: String,
    override val message: String,
    val httpStatus: Int
) : Exception("[$code] $message (HTTP $httpStatus)")

/**
 * The OkHttp clients every [RelayClient] uses. All three derive from one base client, so the whole
 * process shares a single connection pool and dispatcher, however many [RelayClient]s exist.
 */
class RelayHttpClients private constructor(
    /** `GET /v1/agents` and `/v1/history`. */
    val rest: OkHttpClient,
    /** Commands and push registration: a hard cap on the whole call, so a notification action fits in `goAsync()`. */
    val command: OkHttpClient,
    /** `GET /v1/events`: its read timeout is the silence limit ([SSE_SILENCE_TIMEOUT_MS]). */
    val sse: OkHttpClient
) {
    companion object {
        const val CONNECT_TIMEOUT_MS = 10_000L
        const val READ_TIMEOUT_MS = 15_000L
        const val WRITE_TIMEOUT_MS = 10_000L
        const val REST_CALL_TIMEOUT_MS = 15_000L
        const val COMMAND_CALL_TIMEOUT_MS = 8_000L
        /**
         * The relay writes an SSE keepalive every 15 s (contracts §2.3). A stream silent for three of them
         * is a dead (often half-open) socket: the read times out and the engine reconnects.
         */
        const val SSE_SILENCE_TIMEOUT_MS = 45_000L

        val shared: RelayHttpClients by lazy { create() }

        fun create(
            restCallTimeoutMs: Long = REST_CALL_TIMEOUT_MS,
            commandCallTimeoutMs: Long = COMMAND_CALL_TIMEOUT_MS,
            sseSilenceTimeoutMs: Long = SSE_SILENCE_TIMEOUT_MS
        ): RelayHttpClients {
            val base = OkHttpClient.Builder()
                .connectTimeout(CONNECT_TIMEOUT_MS, TimeUnit.MILLISECONDS)
                .readTimeout(READ_TIMEOUT_MS, TimeUnit.MILLISECONDS)
                .writeTimeout(WRITE_TIMEOUT_MS, TimeUnit.MILLISECONDS)
                .build()
            return RelayHttpClients(
                rest = base.newBuilder().callTimeout(restCallTimeoutMs, TimeUnit.MILLISECONDS).build(),
                command = base.newBuilder().callTimeout(commandCallTimeoutMs, TimeUnit.MILLISECONDS).build(),
                sse = base.newBuilder()
                    .connectTimeout(15, TimeUnit.SECONDS)
                    .readTimeout(sseSilenceTimeoutMs, TimeUnit.MILLISECONDS)
                    .retryOnConnectionFailure(true)
                    .build()
            )
        }
    }
}

/**
 * Stateless client for the relay's `/v1` API (contracts §2). Cheap to create: the HTTP machinery
 * lives in [RelayHttpClients.shared].
 *
 * Every suspending call is cancellable: cancelling the coroutine (or a `withTimeout` around it)
 * cancels the OkHttp call at once, and the caller sees the `CancellationException`, never a failure.
 *
 * Any 401 (REST or SSE) is reported with the rejected [token] to [onUnauthorized], or, for clients
 * built without one, to the process-wide [unauthorizedListener] (the repository), which revokes the
 * pairing only if that token is still the stored one.
 */
class RelayClient(
    val baseUrl: String,
    internal val token: String? = null,
    private val http: RelayHttpClients = RelayHttpClients.shared,
    private val onUnauthorized: ((token: String) -> Unit)? = null
) {
    private val gson = Gson()
    private val jsonMediaType = "application/json; charset=utf-8".toMediaType()

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

    private fun <T> decode(response: Response, classOfT: Class<T>): Result<T> {
        val bodyStr = response.body?.string()
        if (!response.isSuccessful) {
            return Result.failure(parseError(response, bodyStr))
        }
        if (classOfT == Unit::class.java) {
            @Suppress("UNCHECKED_CAST")
            return Result.success(Unit as T)
        }
        if (bodyStr.isNullOrBlank()) {
            return Result.failure(RelayError("empty_response", "Empty response from relay", response.code))
        }
        return Result.success(gson.fromJson(bodyStr, classOfT))
    }

    /**
     * Runs [request] asynchronously. Cancelling the coroutine cancels the call; the continuation is
     * then already cancelled, so OkHttp's late "Canceled" failure is dropped.
     */
    private suspend fun <T> executeRequest(
        client: OkHttpClient,
        request: Request,
        classOfT: Class<T>
    ): Result<T> = suspendCancellableCoroutine { cont ->
        val call = client.newCall(request)
        cont.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                cont.resume(Result.failure(asTimeout(e)))
            }

            override fun onResponse(call: Call, response: Response) {
                val result = try {
                    response.use { decode(it, classOfT) }
                } catch (e: IOException) {
                    Result.failure(asTimeout(e))
                } catch (e: RuntimeException) { // malformed JSON (JsonSyntaxException)
                    Result.failure(e)
                }
                if (response.code == 401) reportUnauthorized()
                cont.resume(result)
            }
        })
    }

    suspend fun pair(code: String, deviceName: String): Result<PairResponse> {
        val reqBody = gson.toJson(PairRequest(code = code.trim(), device_name = deviceName)).toRequestBody(jsonMediaType)
        val request = newRequestBuilder("/v1/pair").post(reqBody).build()
        return executeRequest(http.command, request, PairResponse::class.java)
    }

    suspend fun agents(): Result<AgentsSnapshot> {
        val request = newRequestBuilder("/v1/agents").get().build()
        return executeRequest(http.rest, request, AgentsSnapshot::class.java)
    }

    suspend fun history(paneId: String? = null, limit: Int = 20): Result<List<HistoryItem>> {
        var path = "/v1/history?limit=$limit"
        if (!paneId.isNullOrBlank()) {
            path += "&pane_id=${URLEncoder.encode(paneId, "UTF-8")}"
        }
        val request = newRequestBuilder(path).get().build()
        val res = executeRequest(http.rest, request, HistoryResponse::class.java)
        return res.map { it.items }
    }

    suspend fun prompt(paneId: String, text: String, expectedSeq: Long): Result<Unit> {
        val encodedPane = URLEncoder.encode(paneId, "UTF-8")
        val reqBody = gson.toJson(PromptRequest(text = text, expected_seq = expectedSeq)).toRequestBody(jsonMediaType)
        val request = newRequestBuilder("/v1/agents/$encodedPane/prompt").post(reqBody).build()
        return executeRequest(http.command, request, Unit::class.java)
    }

    suspend fun answer(paneId: String, optionId: String, expectedSeq: Long, fingerprint: String): Result<Unit> {
        val encodedPane = URLEncoder.encode(paneId, "UTF-8")
        val reqBody = gson.toJson(AnswerRequest(option_id = optionId, expected_seq = expectedSeq, fingerprint = fingerprint)).toRequestBody(jsonMediaType)
        val request = newRequestBuilder("/v1/agents/$encodedPane/answer").post(reqBody).build()
        return executeRequest(http.command, request, Unit::class.java)
    }

    /**
     * `POST /v1/agents/{pane}/cancel`. [fingerprint] is the prompt's fingerprint when the cancel targets a
     * visible prompt, so the bridge refuses it if the menu changed; null (omitted from the JSON) otherwise.
     */
    suspend fun cancel(paneId: String, expectedSeq: Long, fingerprint: String? = null): Result<Unit> {
        val encodedPane = URLEncoder.encode(paneId, "UTF-8")
        val reqBody = gson.toJson(CancelRequest(expected_seq = expectedSeq, fingerprint = fingerprint)).toRequestBody(jsonMediaType)
        val request = newRequestBuilder("/v1/agents/$encodedPane/cancel").post(reqBody).build()
        return executeRequest(http.command, request, Unit::class.java)
    }

    suspend fun registerPush(fcmToken: String): Result<Unit> {
        val reqBody = gson.toJson(PushRegisterRequest(platform = "fcm", token = fcmToken)).toRequestBody(jsonMediaType)
        val request = newRequestBuilder("/v1/push/register").post(reqBody).build()
        return executeRequest(http.command, request, Unit::class.java)
    }

    /** `GET /v1/events`. A 401 is reported to the unauthorized handler before [listener] sees the failure. */
    fun events(listener: EventSourceListener): EventSource {
        val request = newRequestBuilder("/v1/events")
            .header("Accept", "text/event-stream")
            .get()
            .build()
        val reporting = object : EventSourceListener() {
            override fun onOpen(eventSource: EventSource, response: Response) = listener.onOpen(eventSource, response)
            override fun onEvent(eventSource: EventSource, id: String?, type: String?, data: String) =
                listener.onEvent(eventSource, id, type, data)
            override fun onClosed(eventSource: EventSource) = listener.onClosed(eventSource)
            override fun onFailure(eventSource: EventSource, t: Throwable?, response: Response?) {
                if (response?.code == 401) reportUnauthorized()
                listener.onFailure(eventSource, t, response)
            }
        }
        return EventSources.createFactory(http.sse).newEventSource(request, reporting)
    }

    /** The relay rejected [token] (HTTP 401, `unauthorized`): tell whoever owns the pairing. */
    private fun reportUnauthorized() {
        val rejected = token
        if (rejected.isNullOrBlank()) return // e.g. /v1/pair: no token, nothing was revoked
        (onUnauthorized ?: unauthorizedListener)?.invoke(rejected)
    }

    companion object {
        /**
         * Receives 401s from clients built without an [onUnauthorized] handler (tile, complication,
         * QuickDictate). Set by [RelayRepository.init]; null until then.
         */
        @Volatile
        var unauthorizedListener: ((token: String) -> Unit)? = null

        /**
         * OkHttp's `callTimeout` fails with a bare `InterruptedIOException("timeout")`. Report it as a
         * [SocketTimeoutException] so `commandErrorFeedback` shows "Relay timed out" (the command may
         * still have run) instead of "Can't reach the relay".
         */
        private fun asTimeout(e: IOException): IOException =
            if (e is InterruptedIOException && e !is SocketTimeoutException) {
                SocketTimeoutException(e.message ?: "timeout").also { it.initCause(e) }
            } else {
                e
            }
    }
}
