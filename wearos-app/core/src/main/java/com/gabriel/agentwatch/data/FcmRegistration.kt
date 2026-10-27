package com.gabriel.agentwatch.data

import java.security.MessageDigest

/** What the FCM registrar reads and writes. Android: [Prefs]. */
interface FcmRegistrationStore {
    val relayUrl: String
    val deviceToken: String?
    /** The latest FCM token from Firebase. */
    val fcmToken: String?
    /** The last registration the relay **accepted**, or null. Written only by the registrar. */
    var fcmRegistration: FcmRegistrationRecord?
}

/**
 * The relay accepted [fcmToken] for the pairing identified by [binding] (see [pairingBinding]).
 * Stored as `binding|fcmToken`; the binding is hex, so the first `|` separates the two.
 */
data class FcmRegistrationRecord(val fcmToken: String, val binding: String) {
    fun encode(): String = "$binding|$fcmToken"

    companion object {
        fun decode(stored: String?): FcmRegistrationRecord? {
            if (stored == null) return null
            val i = stored.indexOf('|')
            if (i <= 0 || i == stored.lastIndex) return null
            return FcmRegistrationRecord(fcmToken = stored.substring(i + 1), binding = stored.substring(0, i))
        }
    }
}

/**
 * Identifies one pairing (relay URL + device token) without storing the token again: the first
 * 16 hex chars of SHA-256. Re-pairing creates a new device on the relay, so it needs a new registration
 * even when the FCM token is unchanged.
 */
fun pairingBinding(relayUrl: String, deviceToken: String): String {
    val digest = MessageDigest.getInstance("SHA-256")
        .digest("${relayUrl.trim().trimEnd('/')}\n$deviceToken".toByteArray(Charsets.UTF_8))
    return digest.take(8).joinToString("") { "%02x".format(it) }
}

/** True when [fcmToken] must be sent to the relay: paired, token known, and no accepted record for exactly this pair. */
fun needsFcmRegistration(
    fcmToken: String?,
    relayUrl: String,
    deviceToken: String?,
    record: FcmRegistrationRecord?
): Boolean {
    if (fcmToken.isNullOrBlank() || relayUrl.isBlank() || deviceToken.isNullOrBlank()) return false
    return record == null || record.fcmToken != fcmToken || record.binding != pairingBinding(relayUrl, deviceToken)
}
