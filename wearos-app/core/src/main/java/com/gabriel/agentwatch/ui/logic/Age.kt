package com.gabriel.agentwatch.ui.logic

import java.time.Duration
import java.time.Instant
import java.time.OffsetDateTime

/** How long ago something happened, rounded down for a glance ("5 min ago"). */
sealed interface Age {
    data object JustNow : Age
    data class Minutes(val n: Long) : Age
    data class Hours(val n: Long) : Age
    data class Days(val n: Long) : Age
    data object Unknown : Age
}

/**
 * The age of an RFC 3339 [timestamp] (`updated_at`, `completed_at`) at [now]. A time ahead of [now]
 * (the Mac's clock runs ahead of the watch's) is [Age.JustNow]; an unreadable one is [Age.Unknown].
 */
fun ageOf(timestamp: String, now: Instant): Age {
    val then = try {
        OffsetDateTime.parse(timestamp).toInstant()
    } catch (_: Exception) {
        return Age.Unknown
    }
    val elapsed = Duration.between(then, now)
    return when {
        elapsed.toMinutes() < 1 -> Age.JustNow
        elapsed.toHours() < 1 -> Age.Minutes(elapsed.toMinutes())
        elapsed.toDays() < 1 -> Age.Hours(elapsed.toHours())
        else -> Age.Days(elapsed.toDays())
    }
}
