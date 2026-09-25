package com.gabriel.agentwatch.network

import android.content.Context
import android.util.Log
import com.gabriel.agentwatch.data.FcmRegistrationRecord
import com.gabriel.agentwatch.data.FcmRegistrationStore
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.data.needsFcmRegistration
import com.gabriel.agentwatch.data.pairingBinding
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/**
 * Sends the FCM token to the relay (`POST /v1/push/register`) and records it as registered **only
 * after a 200**. A failure retries on [retryDelaysMs]; after that, the next trigger (app start, stream
 * open, new token) tries again. A 401 is not retried: [RelayClient] already reported it and the pairing
 * is revoked.
 */
class FcmRegistrar(
    private val store: FcmRegistrationStore,
    private val scope: CoroutineScope,
    private val retryDelaysMs: List<Long> = DEFAULT_RETRY_DELAYS_MS,
    private val newClient: (baseUrl: String, token: String) -> RelayClient = { url, token -> RelayClient(url, token) },
    private val log: (String) -> Unit = {}
) {
    enum class Outcome { REGISTERED, ALREADY_REGISTERED, NOT_READY, FAILED, REJECTED }

    private val mutex = Mutex()
    private val retryLock = Any()
    private var retryJob: Job? = null
    private var failures = 0

    /** Registers if the relay does not have this FCM token for this pairing yet. Cheap when nothing is needed. */
    suspend fun ensureRegistered(): Outcome = mutex.withLock {
        val fcmToken = store.fcmToken
        val relayUrl = store.relayUrl
        val deviceToken = store.deviceToken
        if (fcmToken.isNullOrBlank() || relayUrl.isBlank() || deviceToken.isNullOrBlank()) {
            return@withLock Outcome.NOT_READY
        }
        if (!needsFcmRegistration(fcmToken, relayUrl, deviceToken, store.fcmRegistration)) {
            return@withLock Outcome.ALREADY_REGISTERED
        }

        val error = newClient(relayUrl, deviceToken).registerPush(fcmToken).exceptionOrNull()
        when {
            error == null -> {
                // Bound to the pairing the request used: if it changed meanwhile, the next call re-registers.
                store.fcmRegistration = FcmRegistrationRecord(fcmToken, pairingBinding(relayUrl, deviceToken))
                resetRetries()
                log("FCM token registered with the relay")
                Outcome.REGISTERED
            }
            error is RelayError && error.httpStatus == 401 -> {
                log("FCM registration rejected: device token revoked")
                Outcome.REJECTED
            }
            else -> {
                log("FCM registration failed: ${error.javaClass.simpleName}")
                scheduleRetry()
                Outcome.FAILED
            }
        }
    }

    fun ensureRegisteredAsync() {
        scope.launch { ensureRegistered() }
    }

    private fun scheduleRetry() = synchronized(retryLock) {
        if (retryJob?.isActive == true || failures >= retryDelaysMs.size) return@synchronized
        val delayMs = retryDelaysMs[failures++]
        retryJob = scope.launch {
            delay(delayMs)
            synchronized(retryLock) { retryJob = null }
            ensureRegistered()
        }
    }

    private fun resetRetries() = synchronized(retryLock) {
        failures = 0
        retryJob?.cancel()
        retryJob = null
    }

    companion object {
        val DEFAULT_RETRY_DELAYS_MS = listOf(30_000L, 2 * 60_000L, 10 * 60_000L)
    }
}

/**
 * Process-wide entry point for FCM registration. Every path that learns an FCM token or reaches the
 * relay goes through here, so the "registered" record is only ever written on success.
 */
object PushRegistration {
    @Volatile
    private var registrar: FcmRegistrar? = null

    private fun registrar(context: Context): FcmRegistrar =
        registrar ?: synchronized(this) {
            registrar ?: FcmRegistrar(
                store = Prefs(context.applicationContext),
                scope = CoroutineScope(SupervisorJob() + Dispatchers.IO),
                log = { Log.d("FCM", it) }
            ).also { registrar = it }
        }

    /** Firebase issued (or returned) [token]: store it and make sure the relay has it. */
    fun onToken(context: Context, token: String) {
        Prefs(context.applicationContext).fcmToken = token
        ensure(context)
    }

    /** Registers the stored FCM token for the current pairing if the relay does not have it yet. */
    fun ensure(context: Context) {
        RelayRepository.init(context) // so a 401 here revokes the pairing like anywhere else
        registrar(context).ensureRegisteredAsync()
    }
}
